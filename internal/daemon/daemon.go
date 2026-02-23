package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/robmorgan/metamorph/internal/broker"
	"github.com/robmorgan/metamorph/internal/config"
	"github.com/robmorgan/metamorph/internal/constants"
	"github.com/robmorgan/metamorph/internal/docker"
	"github.com/robmorgan/metamorph/internal/gitops"
	"github.com/robmorgan/metamorph/internal/notify"
	"github.com/robmorgan/metamorph/internal/tasks"
)

const (
	monitorInterval       = 30 * time.Second
	staleTaskMaxAge       = 2 * time.Hour
	startupTimeout        = 5 * time.Minute
	shutdownTimeout       = 30 * time.Second
	commitBatchInterval   = 60 * time.Second
	errorDebounceCooldown = 5 * time.Minute
	logTailLines          = 50
)

// State represents the daemon's persisted state.
type State struct {
	Status      string       `json:"status"`
	StartedAt   time.Time    `json:"started_at"`
	ProjectName string       `json:"project_name"`
	Agents      []AgentState `json:"agents"`
	Stats       Stats        `json:"stats"`
}

// AgentState tracks a single agent container.
type AgentState struct {
	ID                int       `json:"id"`
	Role              string    `json:"role"`
	ContainerID       string    `json:"container_id"`
	Status            string    `json:"status"`
	SessionsCompleted int       `json:"sessions_completed"`
	LastActivity      time.Time `json:"last_activity"`
	CurrentTask       *string   `json:"current_task"`
}

// Stats holds aggregate metrics.
type Stats struct {
	TotalCommits   int `json:"total_commits"`
	TotalSessions  int `json:"total_sessions"`
	TasksCompleted int `json:"tasks_completed"`
	UptimeSeconds  int `json:"uptime_seconds"`
}

// Daemon manages the system-wide background server process.
type Daemon struct {
	globalDir  string // ~/.metamorph/
	cfg        *config.GlobalConfig
	apiKey     string
	oauthToken string
	docker     docker.DockerClient
	broker     *broker.Broker
	projects   map[string]*ProjectInstance
	mu         sync.Mutex
	startedAt  time.Time
}

// Start launches the daemon as a background subprocess. It re-execs the
// current binary with "daemon start --daemon-mode" and waits for heartbeat.
func Start(apiKey, oauthToken string) error {
	globalDir := constants.GlobalDir()
	pidPath := filepath.Join(globalDir, constants.DaemonPIDFile)

	if IsRunning() {
		return fmt.Errorf("daemon is already running")
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("daemon: failed to find executable: %w", err)
	}

	args := []string{"daemon", "start", "--daemon-mode"}
	if apiKey != "" {
		args = append(args, "--api-key", apiKey)
	}
	if oauthToken != "" {
		args = append(args, "--oauth-token", oauthToken)
	}
	cmd := exec.Command(exe, args...)
	cmd.Dir = globalDir

	logPath := filepath.Join(globalDir, constants.DaemonLogFile)
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		return fmt.Errorf("daemon: failed to create log dir: %w", err)
	}
	logFile, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("daemon: failed to create log file: %w", err)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("daemon: failed to start subprocess: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(pidPath), 0755); err != nil {
		return fmt.Errorf("daemon: failed to create pid dir: %w", err)
	}
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0644); err != nil {
		return fmt.Errorf("daemon: failed to write pid file: %w", err)
	}

	childPID := cmd.Process.Pid
	_ = cmd.Process.Release()

	// Wait for heartbeat file to appear.
	heartbeatPath := filepath.Join(globalDir, constants.HeartbeatFile)
	deadline := time.Now().Add(startupTimeout)

	tailFile, err := os.Open(logPath)
	if err != nil {
		tailFile = nil
	}
	var reader *bufio.Reader
	if tailFile != nil {
		reader = bufio.NewReader(tailFile)
	}
	printed := make(map[string]bool)

	for time.Now().Before(deadline) {
		if _, err := os.Stat(heartbeatPath); err == nil {
			if tailFile != nil {
				tailFile.Close()
			}
			logFile.Close()
			return nil
		}

		if tailFile != nil {
			for {
				line, err := reader.ReadString('\n')
				if line != "" {
					if msg := parseSlogMsg(line); msg != "" && !printed[msg] {
						printed[msg] = true
						fmt.Println(formatProgressMsg(msg))
					}
				}
				if err != nil {
					break
				}
			}
		}

		if !processAlive(childPID) {
			if tailFile != nil {
				tailFile.Close()
			}
			logFile.Close()
			hint := readDaemonLogHint(logPath)
			return fmt.Errorf("daemon: process exited unexpectedly before becoming ready%s", hint)
		}

		time.Sleep(250 * time.Millisecond)
	}

	if tailFile != nil {
		tailFile.Close()
	}
	logFile.Close()
	hint := readDaemonLogHint(logPath)

	return fmt.Errorf("daemon: timed out waiting for heartbeat after %s%s", startupTimeout, hint)
}

func readDaemonLogHint(logPath string) string {
	logData, err := os.ReadFile(logPath)
	if err != nil || len(logData) == 0 {
		return ""
	}
	return fmt.Sprintf("\n\nDaemon log (%s):\n%s", logPath, string(logData))
}

// Stop sends SIGTERM to the daemon process and waits for it to exit.
func Stop() error {
	globalDir := constants.GlobalDir()
	pidPath := filepath.Join(globalDir, constants.DaemonPIDFile)

	pid, err := readPID(pidPath)
	if err != nil {
		return fmt.Errorf("daemon: %w", err)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		_ = os.Remove(pidPath)
		return fmt.Errorf("daemon: process %d not found: %w", pid, err)
	}

	if err := proc.Signal(syscall.SIGTERM); err != nil {
		_ = os.Remove(pidPath)
		return fmt.Errorf("daemon: failed to send SIGTERM: %w", err)
	}

	deadline := time.Now().Add(shutdownTimeout)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			_ = os.Remove(pidPath)
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	_ = proc.Signal(syscall.SIGKILL)
	time.Sleep(time.Second)
	_ = os.Remove(pidPath)

	return nil
}

// GetStatus reads state.json for a project and verifies the daemon is actually running.
func GetStatus(projectName string) (*State, error) {
	statePath := filepath.Join(constants.ProjectDir(projectName), constants.ProjectStateFile)

	data, err := os.ReadFile(statePath)
	if err != nil {
		return nil, fmt.Errorf("daemon: failed to read state: %w", err)
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("daemon: failed to parse state: %w", err)
	}

	if state.Status == "running" && !IsRunning() {
		state.Status = "stopped"
		for i := range state.Agents {
			if state.Agents[i].Status == "running" {
				state.Agents[i].Status = "stopped"
			}
		}
	}

	return &state, nil
}

// IsRunning checks if the daemon process is alive.
func IsRunning() bool {
	pidPath := filepath.Join(constants.GlobalDir(), constants.DaemonPIDFile)
	pid, err := readPID(pidPath)
	if err != nil {
		return false
	}
	return processAlive(pid)
}

// Run executes the daemon's main loop (called when --daemon-mode is set).
func Run(globalCfg *config.GlobalConfig, apiKey, oauthToken string, dockerClient docker.DockerClient) error {
	globalDir := constants.GlobalDir()

	d := &Daemon{
		globalDir:  globalDir,
		cfg:        globalCfg,
		apiKey:     apiKey,
		oauthToken: oauthToken,
		docker:     dockerClient,
		startedAt:  time.Now().UTC(),
		projects:   make(map[string]*ProjectInstance),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start task broker.
	slog.Info("starting task broker")
	dbPath := constants.BrokerDBPath()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return fmt.Errorf("daemon: failed to create db dir: %w", err)
	}
	store, err := broker.NewSQLiteStore(dbPath)
	if err != nil {
		return fmt.Errorf("daemon: failed to open broker database: %w", err)
	}
	defer store.Close()

	d.broker = broker.New(store)
	brokerAddr := fmt.Sprintf(":%d", globalCfg.Daemon.Port)
	if err := d.broker.Start(brokerAddr); err != nil {
		return fmt.Errorf("daemon: failed to start broker: %w", err)
	}

	// Write initial heartbeat.
	heartbeatPath := filepath.Join(globalDir, constants.HeartbeatFile)
	_ = os.WriteFile(heartbeatPath, []byte(time.Now().UTC().Format(time.RFC3339)), 0644)

	// Set up signal handling.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	ticker := time.NewTicker(monitorInterval)
	defer ticker.Stop()

	slog.Info("daemon ready", "port", globalCfg.Daemon.Port)

	// Start any projects already marked as "running" in the DB.
	d.checkAndStartProjects(ctx)

	for {
		select {
		case <-sigCh:
			return d.shutdown(ctx)
		case <-ticker.C:
			d.monitor(ctx)
		}
	}
}

// StartProject starts agents for a named project.
func (d *Daemon) StartProject(ctx context.Context, projectName string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, exists := d.projects[projectName]; exists {
		return fmt.Errorf("project %q is already running", projectName)
	}

	cfg, err := config.LoadProject(projectName)
	if err != nil {
		return fmt.Errorf("failed to load project config: %w", err)
	}

	instance := NewProjectInstance(projectName, cfg, d.apiKey, d.oauthToken, d.docker)

	// Build image.
	slog.Info("building docker image", "project", projectName)
	if err := d.docker.BuildImage(instance.primaryRepoPath(), cfg.Docker.ExtraPackages); err != nil {
		return fmt.Errorf("failed to build image: %w", err)
	}

	// Start agents.
	slog.Info("starting agents", "project", projectName, "count", cfg.Agents.Count)
	if err := instance.StartAgents(ctx); err != nil {
		return fmt.Errorf("failed to start agents: %w", err)
	}

	// Write state.
	if err := instance.WriteState(); err != nil {
		return fmt.Errorf("failed to write state: %w", err)
	}

	d.projects[projectName] = instance
	return nil
}

// StopProject stops agents for a named project.
func (d *Daemon) StopProject(ctx context.Context, projectName string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	instance, exists := d.projects[projectName]
	if !exists {
		return fmt.Errorf("project %q is not running", projectName)
	}

	instance.StopAgents(ctx)
	delete(d.projects, projectName)
	return nil
}

// monitor runs one iteration of the monitoring loop.
func (d *Daemon) monitor(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("panic in monitor loop (recovered)", "panic", r)
		}
	}()

	now := time.Now().UTC()

	d.mu.Lock()
	projects := make([]*ProjectInstance, 0, len(d.projects))
	for _, p := range d.projects {
		projects = append(projects, p)
	}
	d.mu.Unlock()

	for _, p := range projects {
		d.monitorProject(ctx, p, now)
	}

	// Check for projects that need starting or stopping based on DB status.
	d.checkAndStartProjects(ctx)

	// Write heartbeat.
	heartbeatPath := filepath.Join(d.globalDir, constants.HeartbeatFile)
	_ = os.WriteFile(heartbeatPath, []byte(now.Format(time.RFC3339)), 0644)
}

// monitorProject runs monitoring for a single project.
func (d *Daemon) monitorProject(ctx context.Context, p *ProjectInstance, now time.Time) {
	repoPath := p.primaryRepoPath()
	if repoPath == "" {
		return
	}

	// List running containers.
	agents, err := p.docker.ListAgents(ctx)
	if err == nil {
		d.updateAgentStates(p, agents)
		d.restartCrashedAgents(ctx, p, agents)
	}

	// Update task info.
	d.updateTasks(p)

	// Count commits and notify.
	d.countCommitsAndNotify(p, now)

	// Sync repos when new commits detected.
	if p.hasNewCommits {
		d.syncRepos(p)
		p.hasNewCommits = false
	}

	// Clear stale task locks.
	d.clearStaleTasksAndNotify(p, now)

	// Check agent logs.
	p.CheckAgentLogs(now)

	// Flush commit batch.
	d.flushCommitBatch(p, now)

	// Write state.
	_ = p.WriteState()
}

func (d *Daemon) updateAgentStates(p *ProjectInstance, infos []docker.AgentInfo) {
	infoMap := make(map[int]docker.AgentInfo)
	for _, info := range infos {
		infoMap[info.ID] = info
	}

	for i := range p.Agents {
		a := &p.Agents[i]
		if info, ok := infoMap[a.ID]; ok {
			a.ContainerID = info.ContainerID
			a.Status = normalizeStatus(info.Status)
		} else {
			a.Status = "stopped"
		}
	}
}

func (d *Daemon) restartCrashedAgents(ctx context.Context, p *ProjectInstance, infos []docker.AgentInfo) {
	running := make(map[int]bool)
	for _, info := range infos {
		if strings.Contains(strings.ToLower(info.Status), "up") {
			running[info.ID] = true
		}
	}

	for i := range p.Agents {
		a := &p.Agents[i]
		if running[a.ID] {
			continue
		}

		_ = p.docker.StopAgent(ctx, a.ID)

		brokerURL := fmt.Sprintf("http://host.docker.internal:%d", p.Config.Broker.Port)
		logDir := filepath.Join(p.AgentLogDir(), fmt.Sprintf("agent-%d", a.ID))
		containerID, err := p.docker.StartAgent(ctx, docker.AgentOpts{
			ProjectDir:     p.primaryRepoPath(),
			AgentID:        a.ID,
			Role:           a.Role,
			Model:          p.Config.Agents.Model,
			APIKey:         p.apiKey,
			OAuthToken:     p.oauthToken,
			GitAuthorName:  p.Config.Git.AuthorName,
			GitAuthorEmail: p.Config.Git.AuthorEmail,
			BrokerURL:      brokerURL,
			UpstreamPath:   p.UpstreamRepoPath(),
			LogDir:         logDir,
		})
		if err == nil {
			a.ContainerID = containerID
			a.Status = "running"
			a.LastActivity = time.Now().UTC()

			p.SendEvent(notify.Event{
				Type:      notify.EventAgentCrashed,
				AgentID:   a.ID,
				AgentRole: a.Role,
				Message:   fmt.Sprintf("agent-%d (%s) crashed and was restarted", a.ID, a.Role),
				Timestamp: time.Now().UTC(),
			})
		}
	}
}

func (d *Daemon) updateTasks(p *ProjectInstance) {
	upstreamPath := p.UpstreamRepoPath()
	locks, err := tasks.ListTasks(upstreamPath)
	if err != nil {
		return
	}

	taskMap := make(map[int]string)
	for _, lock := range locks {
		taskMap[lock.AgentID] = lock.Name
	}

	for i := range p.Agents {
		a := &p.Agents[i]
		if name, ok := taskMap[a.ID]; ok {
			a.CurrentTask = &name
		} else {
			a.CurrentTask = nil
		}
	}
}

func (d *Daemon) countCommitsAndNotify(p *ProjectInstance, now time.Time) {
	upstreamPath := p.UpstreamRepoPath()
	cmd := exec.Command("git", "rev-list", "--count", "HEAD")
	cmd.Dir = upstreamPath
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return
	}
	count, err := strconv.Atoi(strings.TrimSpace(out.String()))
	if err != nil {
		return
	}

	newCommits := count - p.prevCommitCount
	if p.prevCommitCount > 0 && newCommits > 0 {
		p.hasNewCommits = true

		logCmd := exec.Command("git", "log", "--oneline", fmt.Sprintf("-%d", newCommits))
		logCmd.Dir = upstreamPath
		var logOut bytes.Buffer
		logCmd.Stdout = &logOut
		if err := logCmd.Run(); err == nil {
			messages := strings.Split(strings.TrimSpace(logOut.String()), "\n")
			if p.commitBatchStart.IsZero() {
				p.commitBatchStart = now
			}
			p.pendingCommits = append(p.pendingCommits, messages...)
		}
	}

	p.prevCommitCount = count
}

func (d *Daemon) flushCommitBatch(p *ProjectInstance, now time.Time) {
	if len(p.pendingCommits) == 0 {
		return
	}
	if now.Sub(p.commitBatchStart) < commitBatchInterval {
		return
	}

	p.SendEvent(notify.Event{
		Type:      notify.EventCommitsPushed,
		Message:   fmt.Sprintf("%d new commit(s) pushed", len(p.pendingCommits)),
		Timestamp: now,
		Details: map[string]interface{}{
			"count":   len(p.pendingCommits),
			"commits": p.pendingCommits,
		},
	})

	p.pendingCommits = nil
	p.commitBatchStart = time.Time{}
}

func (d *Daemon) clearStaleTasksAndNotify(p *ProjectInstance, now time.Time) {
	upstreamPath := p.UpstreamRepoPath()
	cleared, err := tasks.ClearStaleTasks(upstreamPath, staleTaskMaxAge)
	if err != nil {
		return
	}

	for _, taskName := range cleared {
		p.SendEvent(notify.Event{
			Type:      notify.EventStaleLock,
			Message:   fmt.Sprintf("stale task lock cleared: %s", taskName),
			Timestamp: now,
			Details: map[string]interface{}{
				"task": taskName,
			},
		})
	}
}

func (d *Daemon) syncRepos(p *ProjectInstance) {
	upstreamPath := p.UpstreamRepoPath()
	repoPath := p.primaryRepoPath()
	if repoPath == "" {
		return
	}

	workingCopyPath := filepath.Join(p.stateDir, "work")
	if _, err := gitops.SyncToWorkingCopy(upstreamPath, workingCopyPath); err != nil {
		slog.Warn("periodic sync to working copy failed", "project", p.Name, "error", err)
	}

	if _, err := gitops.SyncToProjectDir(upstreamPath, repoPath); err != nil {
		slog.Warn("periodic sync to project dir failed", "project", p.Name, "error", err)
	}
}

// checkAndStartProjects queries the broker DB for projects with status "running"
// that don't have a running ProjectInstance, and starts them. Also stops projects
// marked as "stopped" that still have a running instance.
func (d *Daemon) checkAndStartProjects(ctx context.Context) {
	if d.broker == nil {
		return
	}

	projects, err := d.broker.Store().ListProjects(ctx)
	if err != nil {
		return
	}

	// Collect projects that need starting.
	d.mu.Lock()
	var toStart []string
	var toStop []string
	for _, p := range projects {
		if p.Status == "running" {
			if _, exists := d.projects[p.Name]; !exists {
				toStart = append(toStart, p.Name)
			}
		} else if p.Status == "stopped" {
			if _, exists := d.projects[p.Name]; exists {
				toStop = append(toStop, p.Name)
			}
		}
	}
	d.mu.Unlock()

	for _, name := range toStart {
		if err := d.StartProject(ctx, name); err != nil {
			slog.Error("failed to auto-start project", "project", name, "error", err)
		}
	}

	for _, name := range toStop {
		if err := d.StopProject(ctx, name); err != nil {
			slog.Error("failed to auto-stop project", "project", name, "error", err)
		}
	}
}

func (d *Daemon) shutdown(ctx context.Context) error {
	if d.broker != nil {
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_ = d.broker.Shutdown(shutdownCtx)
		cancel()
	}

	d.mu.Lock()
	for _, p := range d.projects {
		p.StopAgents(ctx)
	}
	d.mu.Unlock()

	// Remove PID file.
	pidPath := filepath.Join(d.globalDir, constants.DaemonPIDFile)
	_ = os.Remove(pidPath)

	return nil
}

// writeStateToPath writes state to a specific path atomically.
func writeStateToPath(path string, state *State) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("daemon: failed to marshal state: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("daemon: failed to create state dir: %w", err)
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("daemon: failed to write temp state: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("daemon: failed to rename state: %w", err)
	}

	return nil
}

// WriteState writes a State to a project's state file.
func WriteState(projectName string, state *State) error {
	path := filepath.Join(constants.ProjectDir(projectName), constants.ProjectStateFile)
	return writeStateToPath(path, state)
}

// readPID reads and parses the PID from a file.
func readPID(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("failed to read pid file: %w", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("invalid pid in %s: %w", path, err)
	}
	return pid, nil
}

func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}

var slogMsgRe = regexp.MustCompile(`msg="([^"]+)"`)

func parseSlogMsg(line string) string {
	m := slogMsgRe.FindStringSubmatch(line)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

func formatProgressMsg(msg string) string {
	if len(msg) == 0 {
		return ""
	}
	return strings.ToUpper(msg[:1]) + msg[1:] + "..."
}

func normalizeStatus(dockerStatus string) string {
	lower := strings.ToLower(dockerStatus)
	switch {
	case strings.Contains(lower, "up"):
		return "running"
	case strings.Contains(lower, "exited"):
		return "exited"
	case strings.Contains(lower, "created"):
		return "created"
	default:
		return "unknown"
	}
}

// Helper functions used by project.go
func splitLines(s string) []string {
	return strings.Split(s, "\n")
}

func containsError(line string) bool {
	return strings.Contains(line, "ERROR:") || strings.Contains(line, "FAIL")
}

func trimSpace(s string) string {
	return strings.TrimSpace(s)
}
