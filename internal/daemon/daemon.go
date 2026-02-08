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
	"syscall"
	"time"

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

// Daemon manages the background server process.
type Daemon struct {
	projectDir string
	cfg        *config.Config
	apiKey     string // Anthropic API key (if set)
	oauthToken string // Claude Code OAuth token (if set)
	docker     docker.DockerClient
	state      *State
	startedAt  time.Time

	// Notification state.
	prevCommitCount   int                  // previous commit count for batching
	commitBatchStart  time.Time            // when the current commit batch started
	pendingCommits    []string             // commit messages accumulated during batch window
	lastErrorNotified map[int]time.Time    // agentID → last time we sent test_failure for this agent
	hasNewCommits     bool                 // true when new commits detected this tick
}

// Start launches the daemon as a background subprocess. It re-execs the
// current binary with --daemon-mode and waits for state.json to appear.
func Start(projectDir string, cfg *config.Config, apiKey, oauthToken string) error {
	pidPath := filepath.Join(projectDir, constants.DaemonPIDFile)

	// Check for orphan containers from a previous crashed daemon.
	if err := cleanOrphans(projectDir, cfg.Project.Name); err != nil {
		return fmt.Errorf("daemon: failed to clean orphans: %w", err)
	}

	// Remove stale state.json so the polling loop doesn't find an old one.
	statePath := filepath.Join(projectDir, constants.StateFile)
	if err := os.Remove(statePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("daemon: failed to remove stale state file: %w", err)
	}

	// Re-exec with --daemon-mode.
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("daemon: failed to find executable: %w", err)
	}

	args := []string{"start", "--daemon-mode", "--project-dir", projectDir}
	if apiKey != "" {
		args = append(args, "--api-key", apiKey)
	}
	if oauthToken != "" {
		args = append(args, "--oauth-token", oauthToken)
	}
	cmd := exec.Command(exe, args...)
	cmd.Dir = projectDir

	// Redirect daemon output to a log file for diagnostics.
	logPath := filepath.Join(projectDir, constants.DaemonLogFile)
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		return fmt.Errorf("daemon: failed to create log dir: %w", err)
	}
	logFile, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("daemon: failed to create log file: %w", err)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	// Detach from parent session.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("daemon: failed to start subprocess: %w", err)
	}

	// Write PID file.
	if err := os.MkdirAll(filepath.Dir(pidPath), 0755); err != nil {
		return fmt.Errorf("daemon: failed to create pid dir: %w", err)
	}
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0644); err != nil {
		return fmt.Errorf("daemon: failed to write pid file: %w", err)
	}

	// Release the child so it outlives us.
	childPID := cmd.Process.Pid
	_ = cmd.Process.Release()

	// Wait for state.json to appear, tailing daemon.log for progress.
	deadline := time.Now().Add(startupTimeout)

	// Open the log file for tailing progress messages.
	tailFile, err := os.Open(logPath)
	if err != nil {
		tailFile = nil // non-fatal: just skip tailing
	}
	var reader *bufio.Reader
	if tailFile != nil {
		reader = bufio.NewReader(tailFile)
	}
	printed := make(map[string]bool) // deduplicate messages

	for time.Now().Before(deadline) {
		if _, err := os.Stat(statePath); err == nil {
			if tailFile != nil {
				tailFile.Close()
			}
			logFile.Close()
			return nil
		}

		// Tail new lines from the daemon log and print progress.
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
					break // io.EOF or other error — wait for more data
				}
			}
		}

		// Check if the child process died before writing state.json.
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

	// Read daemon log for diagnostics.
	if tailFile != nil {
		tailFile.Close()
	}
	logFile.Close()
	hint := readDaemonLogHint(logPath)

	return fmt.Errorf("daemon: timed out waiting for state.json after %s%s", startupTimeout, hint)
}

// readDaemonLogHint reads the daemon log file and returns a formatted hint
// string for inclusion in error messages. Returns empty string on failure.
func readDaemonLogHint(logPath string) string {
	logData, err := os.ReadFile(logPath)
	if err != nil || len(logData) == 0 {
		return ""
	}
	return fmt.Sprintf("\n\nDaemon log (%s):\n%s", logPath, string(logData))
}

// Stop sends SIGTERM to the daemon process and waits for it to exit.
func Stop(projectDir string) error {
	pidPath := filepath.Join(projectDir, constants.DaemonPIDFile)

	pid, err := readPID(pidPath)
	if err != nil {
		return fmt.Errorf("daemon: %w", err)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		_ = os.Remove(pidPath)
		return fmt.Errorf("daemon: process %d not found: %w", pid, err)
	}

	// Send SIGTERM.
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		_ = os.Remove(pidPath)
		return fmt.Errorf("daemon: failed to send SIGTERM: %w", err)
	}

	// Wait for exit.
	deadline := time.Now().Add(shutdownTimeout)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			_ = os.Remove(pidPath)
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Force kill.
	_ = proc.Signal(syscall.SIGKILL)
	time.Sleep(time.Second)
	_ = os.Remove(pidPath)

	return nil
}

// GetStatus reads state.json and verifies the daemon is actually running.
func GetStatus(projectDir string) (*State, error) {
	statePath := filepath.Join(projectDir, constants.StateFile)

	data, err := os.ReadFile(statePath)
	if err != nil {
		return nil, fmt.Errorf("daemon: failed to read state: %w", err)
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("daemon: failed to parse state: %w", err)
	}

	// Verify the daemon PID is actually alive.
	if state.Status == "running" && !IsRunning(projectDir) {
		state.Status = "stopped"
		// Also mark all agents as stopped since the daemon is dead.
		for i := range state.Agents {
			if state.Agents[i].Status == "running" {
				state.Agents[i].Status = "stopped"
			}
		}
	}

	return &state, nil
}

// IsRunning checks if the daemon process is alive.
func IsRunning(projectDir string) bool {
	pidPath := filepath.Join(projectDir, constants.DaemonPIDFile)
	pid, err := readPID(pidPath)
	if err != nil {
		return false
	}
	return processAlive(pid)
}

// Run executes the daemon's main loop (called when --daemon-mode is set).
// This is exported so the CLI can call it from the start command.
func Run(projectDir string, cfg *config.Config, apiKey, oauthToken string, dockerClient docker.DockerClient) error {
	d := &Daemon{
		projectDir:        projectDir,
		cfg:               cfg,
		apiKey:            apiKey,
		oauthToken:        oauthToken,
		docker:            dockerClient,
		startedAt:         time.Now().UTC(),
		lastErrorNotified: make(map[int]time.Time),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Build image.
	slog.Info("building docker image")
	if err := d.docker.BuildImage(projectDir); err != nil {
		return fmt.Errorf("daemon: failed to build image: %w", err)
	}

	// Start agents.
	slog.Info("starting agents")
	agentStates, err := d.startAgents(ctx)
	if err != nil {
		return fmt.Errorf("daemon: failed to start agents: %w", err)
	}

	// Write initial state.
	d.state = &State{
		Status:      "running",
		StartedAt:   d.startedAt,
		ProjectName: cfg.Project.Name,
		Agents:      agentStates,
	}
	if err := d.writeState(); err != nil {
		return fmt.Errorf("daemon: failed to write initial state: %w", err)
	}

	// Set up signal handling.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	ticker := time.NewTicker(monitorInterval)
	defer ticker.Stop()

	for {
		select {
		case <-sigCh:
			return d.shutdown(ctx)
		case <-ticker.C:
			d.monitor(ctx)
		}
	}
}

// startAgents creates containers for all configured agents.
func (d *Daemon) startAgents(ctx context.Context) ([]AgentState, error) {
	var agents []AgentState

	roles := d.cfg.Agents.Roles
	for i := 1; i <= d.cfg.Agents.Count; i++ {
		role := "developer"
		if len(roles) > 0 {
			role = roles[(i-1)%len(roles)]
		}

		slog.Info("starting agent", "agent", i, "role", role)
		containerID, err := d.docker.StartAgent(ctx, docker.AgentOpts{
			ProjectDir:     d.projectDir,
			AgentID:        i,
			Role:           role,
			Model:          d.cfg.Agents.Model,
			APIKey:         d.apiKey,
			OAuthToken:     d.oauthToken,
			GitAuthorName:  d.cfg.Git.AuthorName,
			GitAuthorEmail: d.cfg.Git.AuthorEmail,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to start agent-%d: %w", i, err)
		}

		agents = append(agents, AgentState{
			ID:           i,
			Role:         role,
			ContainerID:  containerID,
			Status:       "running",
			LastActivity: time.Now().UTC(),
		})
	}

	return agents, nil
}

// monitor runs one iteration of the monitoring loop, recovering from panics.
func (d *Daemon) monitor(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("panic in monitor loop (recovered)", "panic", r)
		}
	}()

	now := time.Now().UTC()

	// List running containers.
	agents, err := d.docker.ListAgents(ctx)
	if err == nil {
		d.updateAgentStates(agents)
		d.restartCrashedAgents(ctx, agents)
	}

	// Update task info.
	d.updateTasks()

	// Count commits and notify if new ones detected.
	d.countCommitsAndNotify(now)

	// Sync repos when new commits are detected.
	if d.hasNewCommits {
		d.syncRepos()
		d.hasNewCommits = false
	}

	// Clear stale task locks and notify.
	d.clearStaleTasksAndNotify(now)

	// Check agent logs for errors.
	d.checkAgentLogs(now)

	// Flush pending commit batch if window has elapsed.
	d.flushCommitBatch(now)

	// Update uptime.
	d.state.Stats.UptimeSeconds = int(now.Sub(d.startedAt).Seconds())

	// Write state atomically.
	_ = d.writeState()

	// Write heartbeat.
	heartbeatPath := filepath.Join(d.projectDir, constants.HeartbeatFile)
	_ = os.WriteFile(heartbeatPath, []byte(now.Format(time.RFC3339)), 0644)
}

// updateAgentStates syncs container status into agent state.
func (d *Daemon) updateAgentStates(infos []docker.AgentInfo) {
	infoMap := make(map[int]docker.AgentInfo)
	for _, info := range infos {
		infoMap[info.ID] = info
	}

	for i := range d.state.Agents {
		a := &d.state.Agents[i]
		if info, ok := infoMap[a.ID]; ok {
			a.ContainerID = info.ContainerID
			a.Status = normalizeStatus(info.Status)
		} else {
			a.Status = "stopped"
		}
	}
}

// restartCrashedAgents restarts any agents that are no longer running.
func (d *Daemon) restartCrashedAgents(ctx context.Context, infos []docker.AgentInfo) {
	running := make(map[int]bool)
	for _, info := range infos {
		if strings.Contains(strings.ToLower(info.Status), "up") {
			running[info.ID] = true
		}
	}

	for i := range d.state.Agents {
		a := &d.state.Agents[i]
		if running[a.ID] {
			continue
		}

		// Try to stop cleanly first (removes exited container).
		_ = d.docker.StopAgent(ctx, a.ID)

		// Restart.
		containerID, err := d.docker.StartAgent(ctx, docker.AgentOpts{
			ProjectDir:     d.projectDir,
			AgentID:        a.ID,
			Role:           a.Role,
			Model:          d.cfg.Agents.Model,
			APIKey:         d.apiKey,
			OAuthToken:     d.oauthToken,
			GitAuthorName:  d.cfg.Git.AuthorName,
			GitAuthorEmail: d.cfg.Git.AuthorEmail,
		})
		if err == nil {
			a.ContainerID = containerID
			a.Status = "running"
			a.LastActivity = time.Now().UTC()

			// Notify about the crash/restart.
			d.sendEvent(notify.Event{
				Type:      notify.EventAgentCrashed,
				AgentID:   a.ID,
				AgentRole: a.Role,
				Project:   d.cfg.Project.Name,
				Message:   fmt.Sprintf("agent-%d (%s) crashed and was restarted", a.ID, a.Role),
				Timestamp: time.Now().UTC(),
			})
		}
	}
}

// updateTasks reads current task locks and maps them to agents.
func (d *Daemon) updateTasks() {
	upstreamPath := filepath.Join(d.projectDir, constants.UpstreamDir)
	locks, err := tasks.ListTasks(upstreamPath)
	if err != nil {
		return
	}

	taskMap := make(map[int]string)
	for _, lock := range locks {
		taskMap[lock.AgentID] = lock.Name
	}

	for i := range d.state.Agents {
		a := &d.state.Agents[i]
		if name, ok := taskMap[a.ID]; ok {
			a.CurrentTask = &name
		} else {
			a.CurrentTask = nil
		}
	}
}

// countCommitsAndNotify counts total commits and accumulates new ones for batched notification.
func (d *Daemon) countCommitsAndNotify(now time.Time) {
	upstreamPath := filepath.Join(d.projectDir, constants.UpstreamDir)
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

	newCommits := count - d.prevCommitCount
	if d.prevCommitCount > 0 && newCommits > 0 {
		d.hasNewCommits = true

		// Read the latest commit messages.
		logCmd := exec.Command("git", "log", "--oneline", fmt.Sprintf("-%d", newCommits))
		logCmd.Dir = upstreamPath
		var logOut bytes.Buffer
		logCmd.Stdout = &logOut
		if err := logCmd.Run(); err == nil {
			messages := strings.Split(strings.TrimSpace(logOut.String()), "\n")
			if d.commitBatchStart.IsZero() {
				d.commitBatchStart = now
			}
			d.pendingCommits = append(d.pendingCommits, messages...)
		}
	}

	d.prevCommitCount = count
	d.state.Stats.TotalCommits = count
}

// flushCommitBatch sends a batched commits_pushed notification if the batch window has elapsed.
func (d *Daemon) flushCommitBatch(now time.Time) {
	if len(d.pendingCommits) == 0 {
		return
	}
	if now.Sub(d.commitBatchStart) < commitBatchInterval {
		return
	}

	d.sendEvent(notify.Event{
		Type:      notify.EventCommitsPushed,
		Project:   d.cfg.Project.Name,
		Message:   fmt.Sprintf("%d new commit(s) pushed", len(d.pendingCommits)),
		Timestamp: now,
		Details: map[string]interface{}{
			"count":   len(d.pendingCommits),
			"commits": d.pendingCommits,
		},
	})

	d.pendingCommits = nil
	d.commitBatchStart = time.Time{}
}

// clearStaleTasksAndNotify removes stale task locks and sends notifications.
func (d *Daemon) clearStaleTasksAndNotify(now time.Time) {
	upstreamPath := filepath.Join(d.projectDir, constants.UpstreamDir)
	cleared, err := tasks.ClearStaleTasks(upstreamPath, staleTaskMaxAge)
	if err != nil {
		return
	}
	d.state.Stats.TasksCompleted += len(cleared)

	for _, taskName := range cleared {
		d.sendEvent(notify.Event{
			Type:      notify.EventStaleLock,
			Project:   d.cfg.Project.Name,
			Message:   fmt.Sprintf("stale task lock cleared: %s", taskName),
			Timestamp: now,
			Details: map[string]interface{}{
				"task": taskName,
			},
		})
	}
}

// checkAgentLogs scans each agent's latest log file for ERROR:/FAIL lines.
func (d *Daemon) checkAgentLogs(now time.Time) {
	for _, a := range d.state.Agents {
		// Debounce: skip if we notified about this agent recently.
		if lastNotified, ok := d.lastErrorNotified[a.ID]; ok {
			if now.Sub(lastNotified) < errorDebounceCooldown {
				continue
			}
		}

		logDir := filepath.Join(d.projectDir, constants.AgentLogDir, fmt.Sprintf("agent-%d", a.ID))
		entries, err := os.ReadDir(logDir)
		if err != nil {
			continue
		}

		// Find the latest session log.
		var latestLog string
		var latestNum int
		for _, e := range entries {
			name := e.Name()
			if !strings.HasPrefix(name, "session-") || !strings.HasSuffix(name, ".log") {
				continue
			}
			numStr := strings.TrimSuffix(strings.TrimPrefix(name, "session-"), ".log")
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

		lines := strings.Split(string(data), "\n")
		start := 0
		if len(lines) > logTailLines {
			start = len(lines) - logTailLines
		}

		for _, line := range lines[start:] {
			if strings.Contains(line, "ERROR:") || strings.Contains(line, "FAIL") {
				d.lastErrorNotified[a.ID] = now
				d.sendEvent(notify.Event{
					Type:      notify.EventTestFailure,
					AgentID:   a.ID,
					AgentRole: a.Role,
					Project:   d.cfg.Project.Name,
					Message:   fmt.Sprintf("error detected in agent-%d logs", a.ID),
					Timestamp: now,
					Details: map[string]interface{}{
						"line": strings.TrimSpace(line),
					},
				})
				break // one notification per agent per check
			}
		}
	}
}

// sendEvent sends a notification event, logging any errors.
func (d *Daemon) sendEvent(event notify.Event) {
	webhookURL := d.cfg.Notifications.WebhookURL
	if webhookURL == "" {
		return
	}
	if err := notify.Send(webhookURL, event); err != nil {
		slog.Error("failed to send notification", "event", event.Type, "error", err)
	}
}

// syncRepos syncs the upstream bare repo to both the working copy and the
// user's project directory so changes are visible without running `metamorph sync`.
func (d *Daemon) syncRepos() {
	upstreamPath := filepath.Join(d.projectDir, constants.UpstreamDir)
	workingCopyPath := filepath.Join(d.projectDir, ".metamorph", "work")

	if _, err := gitops.SyncToWorkingCopy(upstreamPath, workingCopyPath); err != nil {
		slog.Warn("periodic sync to working copy failed", "error", err)
	}

	if _, err := gitops.SyncToProjectDir(upstreamPath, d.projectDir); err != nil {
		slog.Warn("periodic sync to project dir failed", "error", err)
	}
}

// shutdown stops all agents and writes final state.
func (d *Daemon) shutdown(ctx context.Context) error {
	_ = d.docker.StopAllAgents(ctx)

	// Final sync so the latest agent work is visible in the project dir.
	d.syncRepos()

	d.state.Status = "stopped"
	d.state.Stats.UptimeSeconds = int(time.Since(d.startedAt).Seconds())
	_ = d.writeState()

	// Remove PID file.
	pidPath := filepath.Join(d.projectDir, constants.DaemonPIDFile)
	_ = os.Remove(pidPath)

	return nil
}

// writeState writes state.json atomically via temp file + rename.
func (d *Daemon) writeState() error {
	return WriteState(d.projectDir, d.state)
}

// WriteState writes a State to state.json atomically.
func WriteState(projectDir string, state *State) error {
	statePath := filepath.Join(projectDir, constants.StateFile)

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("daemon: failed to marshal state: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(statePath), 0755); err != nil {
		return fmt.Errorf("daemon: failed to create state dir: %w", err)
	}

	tmpPath := statePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("daemon: failed to write temp state: %w", err)
	}

	if err := os.Rename(tmpPath, statePath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("daemon: failed to rename state: %w", err)
	}

	return nil
}

// cleanOrphans stops containers from a previous crashed daemon.
func cleanOrphans(projectDir string, projectName string) error {
	if IsRunning(projectDir) {
		return fmt.Errorf("daemon is already running")
	}

	dc, err := docker.NewClient(projectName)
	if err != nil {
		// Docker not available — nothing to clean.
		return nil
	}

	agents, err := dc.ListAgents(context.Background())
	if err != nil {
		return nil
	}

	if len(agents) > 0 {
		_ = dc.StopAllAgents(context.Background())
	}

	return nil
}

// CleanOrphansWithClient is like cleanOrphans but accepts a DockerClient
// for testing.
func CleanOrphansWithClient(projectDir string, dc docker.DockerClient) error {
	if IsRunning(projectDir) {
		return fmt.Errorf("daemon is already running")
	}

	agents, err := dc.ListAgents(context.Background())
	if err != nil {
		return nil
	}

	if len(agents) > 0 {
		_ = dc.StopAllAgents(context.Background())
	}

	return nil
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

// processAlive checks if a process with the given PID exists.
func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, FindProcess always succeeds. Signal 0 checks existence.
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}

// slogMsgRe matches the msg="..." field in slog text output.
var slogMsgRe = regexp.MustCompile(`msg="([^"]+)"`)

// parseSlogMsg extracts the msg value from a slog text-format log line.
func parseSlogMsg(line string) string {
	m := slogMsgRe.FindStringSubmatch(line)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

// formatProgressMsg converts a slog msg value into a user-friendly progress string.
func formatProgressMsg(msg string) string {
	// Capitalize first letter and add ellipsis.
	if len(msg) == 0 {
		return ""
	}
	return strings.ToUpper(msg[:1]) + msg[1:] + "..."
}

// normalizeStatus converts Docker status strings to our simpler model.
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
