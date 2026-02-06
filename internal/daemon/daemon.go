package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/brightfame/metamorph/internal/config"
	"github.com/brightfame/metamorph/internal/constants"
	"github.com/brightfame/metamorph/internal/docker"
	"github.com/brightfame/metamorph/internal/tasks"
)

const (
	monitorInterval  = 30 * time.Second
	staleTaskMaxAge  = 2 * time.Hour
	startupTimeout   = 10 * time.Second
	shutdownTimeout  = 30 * time.Second
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
	apiKey     string
	docker     docker.DockerClient
	state      *State
	startedAt  time.Time
}

// Start launches the daemon as a background subprocess. It re-execs the
// current binary with --daemon-mode and waits for state.json to appear.
func Start(projectDir string, cfg *config.Config, apiKey string) error {
	pidPath := filepath.Join(projectDir, constants.DaemonPIDFile)

	// Check for orphan containers from a previous crashed daemon.
	if err := cleanOrphans(projectDir, cfg.Project.Name); err != nil {
		return fmt.Errorf("daemon: failed to clean orphans: %w", err)
	}

	// Re-exec with --daemon-mode.
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("daemon: failed to find executable: %w", err)
	}

	cmd := exec.Command(exe, "start", "--daemon-mode",
		"--project-dir", projectDir,
		"--api-key", apiKey,
	)
	cmd.Dir = projectDir
	cmd.Stdout = nil
	cmd.Stderr = nil
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
	cmd.Process.Release()

	// Wait for state.json to appear.
	statePath := filepath.Join(projectDir, constants.StateFile)
	deadline := time.Now().Add(startupTimeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(statePath); err == nil {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}

	return fmt.Errorf("daemon: timed out waiting for state.json after %s", startupTimeout)
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
		os.Remove(pidPath)
		return fmt.Errorf("daemon: process %d not found: %w", pid, err)
	}

	// Send SIGTERM.
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		os.Remove(pidPath)
		return fmt.Errorf("daemon: failed to send SIGTERM: %w", err)
	}

	// Wait for exit.
	deadline := time.Now().Add(shutdownTimeout)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			os.Remove(pidPath)
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Force kill.
	proc.Signal(syscall.SIGKILL)
	time.Sleep(time.Second)
	os.Remove(pidPath)

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
func Run(projectDir string, cfg *config.Config, apiKey string, dockerClient docker.DockerClient) error {
	d := &Daemon{
		projectDir: projectDir,
		cfg:        cfg,
		apiKey:     apiKey,
		docker:     dockerClient,
		startedAt:  time.Now().UTC(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Build image.
	if err := d.docker.BuildImage(projectDir); err != nil {
		return fmt.Errorf("daemon: failed to build image: %w", err)
	}

	// Start agents.
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

		containerID, err := d.docker.StartAgent(ctx, docker.AgentOpts{
			ProjectDir: d.projectDir,
			AgentID:    i,
			Role:       role,
			Model:      d.cfg.Agents.Model,
			APIKey:     d.apiKey,
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

// monitor runs one iteration of the monitoring loop.
func (d *Daemon) monitor(ctx context.Context) {
	now := time.Now().UTC()

	// List running containers.
	agents, err := d.docker.ListAgents(ctx)
	if err == nil {
		d.updateAgentStates(agents)
		d.restartCrashedAgents(ctx, agents)
	}

	// Update task info.
	d.updateTasks()

	// Count commits.
	d.countCommits()

	// Clear stale task locks.
	d.clearStaleTasks()

	// Update uptime.
	d.state.Stats.UptimeSeconds = int(now.Sub(d.startedAt).Seconds())

	// Write state atomically.
	d.writeState()

	// Write heartbeat.
	heartbeatPath := filepath.Join(d.projectDir, constants.HeartbeatFile)
	os.WriteFile(heartbeatPath, []byte(now.Format(time.RFC3339)), 0644)
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
		d.docker.StopAgent(ctx, a.ID)

		// Restart.
		containerID, err := d.docker.StartAgent(ctx, docker.AgentOpts{
			ProjectDir: d.projectDir,
			AgentID:    a.ID,
			Role:       a.Role,
			Model:      d.cfg.Agents.Model,
			APIKey:     d.apiKey,
		})
		if err == nil {
			a.ContainerID = containerID
			a.Status = "running"
			a.LastActivity = time.Now().UTC()
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

// countCommits counts total commits in the upstream repo.
func (d *Daemon) countCommits() {
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
	d.state.Stats.TotalCommits = count
}

// clearStaleTasks removes task locks older than the threshold.
func (d *Daemon) clearStaleTasks() {
	upstreamPath := filepath.Join(d.projectDir, constants.UpstreamDir)
	cleared, err := tasks.ClearStaleTasks(upstreamPath, staleTaskMaxAge)
	if err != nil {
		return
	}
	d.state.Stats.TasksCompleted += len(cleared)
}

// shutdown stops all agents and writes final state.
func (d *Daemon) shutdown(ctx context.Context) error {
	d.docker.StopAllAgents(ctx)

	d.state.Status = "stopped"
	d.state.Stats.UptimeSeconds = int(time.Since(d.startedAt).Seconds())
	d.writeState()

	// Remove PID file.
	pidPath := filepath.Join(d.projectDir, constants.DaemonPIDFile)
	os.Remove(pidPath)

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
		os.Remove(tmpPath)
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
		dc.StopAllAgents(context.Background())
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
		dc.StopAllAgents(context.Background())
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
