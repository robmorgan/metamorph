package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/robmorgan/metamorph/internal/config"
	"github.com/robmorgan/metamorph/internal/constants"
	"github.com/robmorgan/metamorph/internal/docker"
	"github.com/robmorgan/metamorph/internal/notify"
)

// ProjectInstance manages agents for a single project within the multi-project daemon.
type ProjectInstance struct {
	Name       string
	Config     *config.Config
	Agents     []AgentState
	Repos      []config.RepoConfig
	stateDir   string // ~/.metamorph/projects/{name}/
	apiKey     string
	oauthToken string
	docker     docker.DockerClient
	startedAt  time.Time

	// Notification state.
	prevCommitCount   int
	commitBatchStart  time.Time
	pendingCommits    []string
	lastErrorNotified map[int]time.Time
	hasNewCommits     bool
}

// NewProjectInstance creates a project instance ready to start agents.
func NewProjectInstance(name string, cfg *config.Config, apiKey, oauthToken string, dockerClient docker.DockerClient) *ProjectInstance {
	return &ProjectInstance{
		Name:              name,
		Config:            cfg,
		Repos:             cfg.Repos,
		stateDir:          constants.ProjectDir(name),
		apiKey:            apiKey,
		oauthToken:        oauthToken,
		docker:            dockerClient,
		startedAt:         time.Now().UTC(),
		lastErrorNotified: make(map[int]time.Time),
	}
}

// StartAgents creates containers for all configured agents.
func (p *ProjectInstance) StartAgents(ctx context.Context) error {
	roles := p.Config.Agents.Roles
	for i := 1; i <= p.Config.Agents.Count; i++ {
		role := "developer"
		if len(roles) > 0 {
			role = roles[(i-1)%len(roles)]
		}

		brokerURL := fmt.Sprintf("http://host.docker.internal:%d", p.Config.Broker.Port)

		// Use the first repo path as the project directory for Docker mounts.
		projectDir := p.primaryRepoPath()

		logDir := filepath.Join(p.AgentLogDir(), fmt.Sprintf("agent-%d", i))

		slog.Info("starting agent", "project", p.Name, "agent", i, "role", role)
		containerID, err := p.docker.StartAgent(ctx, docker.AgentOpts{
			ProjectDir:     projectDir,
			AgentID:        i,
			Role:           role,
			Model:          p.Config.Agents.Model,
			APIKey:         p.apiKey,
			OAuthToken:     p.oauthToken,
			GitAuthorName:  p.Config.Git.AuthorName,
			GitAuthorEmail: p.Config.Git.AuthorEmail,
			BrokerURL:      brokerURL,
			UpstreamPaths:  p.UpstreamRepoPaths(),
			LogDir:         logDir,
		})
		if err != nil {
			return fmt.Errorf("failed to start agent-%d: %w", i, err)
		}

		p.Agents = append(p.Agents, AgentState{
			ID:           i,
			Role:         role,
			ContainerID:  containerID,
			Status:       "running",
			LastActivity: time.Now().UTC(),
		})
	}

	return nil
}

// StopAgents stops all agent containers for this project.
func (p *ProjectInstance) StopAgents(ctx context.Context) {
	_ = p.docker.StopAllAgents(ctx)
}

// WriteState writes project state to disk.
func (p *ProjectInstance) WriteState() error {
	state := &State{
		Status:      "running",
		StartedAt:   p.startedAt,
		ProjectName: p.Name,
		Agents:      p.Agents,
	}

	statePath := filepath.Join(p.stateDir, constants.ProjectStateFile)
	return writeStateToPath(statePath, state)
}

// AgentLogDir returns the path to agent logs for this project.
func (p *ProjectInstance) AgentLogDir() string {
	return filepath.Join(p.stateDir, constants.AgentLogDir)
}

// UpstreamRepoPaths returns all upstream repo mounts for this project.
func (p *ProjectInstance) UpstreamRepoPaths() []docker.UpstreamMount {
	var mounts []docker.UpstreamMount
	for _, repo := range p.Repos {
		label := config.RepoLabel(repo)
		mounts = append(mounts, docker.UpstreamMount{
			Label: label,
			Path:  constants.UpstreamRepoPath(p.Name, label),
		})
	}
	return mounts
}

// PrimaryUpstreamRepoPath returns the upstream bare repo path for the primary (first) repo.
func (p *ProjectInstance) PrimaryUpstreamRepoPath() string {
	if len(p.Repos) == 0 {
		return ""
	}
	label := config.RepoLabel(p.Repos[0])
	return constants.UpstreamRepoPath(p.Name, label)
}

// primaryRepoPath returns the first registered repo path (the "main" project dir).
func (p *ProjectInstance) primaryRepoPath() string {
	if len(p.Repos) > 0 {
		return p.Repos[0].Path
	}
	return ""
}

// SendEvent sends a notification event for this project.
func (p *ProjectInstance) SendEvent(event notify.Event) {
	event.Project = p.Name
	webhookURL := p.Config.Notifications.WebhookURL
	if webhookURL == "" {
		return
	}
	if err := notify.Send(webhookURL, event); err != nil {
		slog.Error("failed to send notification", "event", event.Type, "error", err)
	}
}

// CheckAgentLogs scans each agent's latest log file for ERROR:/FAIL lines.
func (p *ProjectInstance) CheckAgentLogs(now time.Time) {
	for _, a := range p.Agents {
		if lastNotified, ok := p.lastErrorNotified[a.ID]; ok {
			if now.Sub(lastNotified) < errorDebounceCooldown {
				continue
			}
		}

		logDir := filepath.Join(p.AgentLogDir(), fmt.Sprintf("agent-%d", a.ID))
		entries, err := os.ReadDir(logDir)
		if err != nil {
			continue
		}

		var latestLog string
		var latestNum int
		for _, e := range entries {
			name := e.Name()
			if len(name) < 12 {
				continue
			}
			numStr := name[8 : len(name)-4] // session-N.log
			num, err := strconv.Atoi(numStr)
			if err != nil {
				continue
			}
			if num > latestNum {
				latestNum = num
				latestLog = filepath.Join(logDir, name)
			}
		}

		if latestLog == "" {
			continue
		}

		data, err := os.ReadFile(latestLog)
		if err != nil {
			continue
		}

		lines := splitLines(string(data))
		start := 0
		if len(lines) > logTailLines {
			start = len(lines) - logTailLines
		}

		for _, line := range lines[start:] {
			if containsError(line) {
				p.lastErrorNotified[a.ID] = now
				p.SendEvent(notify.Event{
					Type:      notify.EventTestFailure,
					AgentID:   a.ID,
					AgentRole: a.Role,
					Message:   fmt.Sprintf("error detected in agent-%d logs", a.ID),
					Timestamp: now,
					Details: map[string]interface{}{
						"line": trimSpace(line),
					},
				})
				break
			}
		}
	}
}
