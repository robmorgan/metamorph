package daemon

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/robmorgan/metamorph/internal/config"
	"github.com/robmorgan/metamorph/internal/constants"
	"github.com/robmorgan/metamorph/internal/docker"
	"github.com/robmorgan/metamorph/internal/notify"
)

// mockDockerClient implements docker.DockerClient for daemon tests.
type mockDockerClient struct {
	buildErr    error
	startAgents map[int]string // agentID -> containerID
	startErr    error
	stopCalls   []int
	stopAllCall bool
	stopErr     error
	listResult  []docker.AgentInfo
	listErr     error
	logsBody    string
	logsErr     error
}

func (m *mockDockerClient) BuildImage(projectDir string) error {
	return m.buildErr
}

func (m *mockDockerClient) StartAgent(ctx context.Context, opts docker.AgentOpts) (string, error) {
	if m.startErr != nil {
		return "", m.startErr
	}
	cid := "mock-container-" + strconv.Itoa(opts.AgentID)
	if m.startAgents != nil {
		m.startAgents[opts.AgentID] = cid
	}
	return cid, nil
}

func (m *mockDockerClient) StopAgent(ctx context.Context, agentID int) error {
	m.stopCalls = append(m.stopCalls, agentID)
	return m.stopErr
}

func (m *mockDockerClient) StopAllAgents(ctx context.Context) error {
	m.stopAllCall = true
	return m.stopErr
}

func (m *mockDockerClient) ListAgents(ctx context.Context) ([]docker.AgentInfo, error) {
	return m.listResult, m.listErr
}

func (m *mockDockerClient) GetLogs(ctx context.Context, agentID int, tail int, follow bool) (io.ReadCloser, error) {
	if m.logsErr != nil {
		return nil, m.logsErr
	}
	return io.NopCloser(strings.NewReader(m.logsBody)), nil
}

// --- State Serialization Tests ---

func TestWriteState(t *testing.T) {
	t.Run("writes valid JSON to state.json", func(t *testing.T) {
		dir := t.TempDir()
		_ = os.MkdirAll(filepath.Join(dir, ".metamorph"), 0755)

		taskName := "implement-auth"
		state := &State{
			Status:      "running",
			StartedAt:   time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
			ProjectName: "test-project",
			Agents: []AgentState{
				{
					ID:                1,
					Role:              "developer",
					ContainerID:       "abc123",
					Status:            "running",
					SessionsCompleted: 3,
					LastActivity:      time.Date(2025, 6, 15, 11, 0, 0, 0, time.UTC),
					CurrentTask:       &taskName,
				},
			},
			Stats: Stats{
				TotalCommits:   42,
				TotalSessions:  10,
				TasksCompleted: 5,
				UptimeSeconds:  3600,
			},
		}

		if err := WriteState(dir, state); err != nil {
			t.Fatalf("WriteState: %v", err)
		}

		// Read and verify.
		statePath := filepath.Join(dir, constants.StateFile)
		data, err := os.ReadFile(statePath)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}

		var got State
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}

		if got.Status != "running" {
			t.Errorf("Status = %q, want %q", got.Status, "running")
		}
		if got.ProjectName != "test-project" {
			t.Errorf("ProjectName = %q", got.ProjectName)
		}
		if len(got.Agents) != 1 {
			t.Fatalf("len(Agents) = %d, want 1", len(got.Agents))
		}
		if got.Agents[0].ID != 1 {
			t.Errorf("Agent.ID = %d", got.Agents[0].ID)
		}
		if got.Agents[0].ContainerID != "abc123" {
			t.Errorf("Agent.ContainerID = %q", got.Agents[0].ContainerID)
		}
		if got.Agents[0].CurrentTask == nil || *got.Agents[0].CurrentTask != "implement-auth" {
			t.Errorf("Agent.CurrentTask = %v", got.Agents[0].CurrentTask)
		}
		if got.Stats.TotalCommits != 42 {
			t.Errorf("Stats.TotalCommits = %d", got.Stats.TotalCommits)
		}
		if got.Stats.UptimeSeconds != 3600 {
			t.Errorf("Stats.UptimeSeconds = %d", got.Stats.UptimeSeconds)
		}
	})

	t.Run("creates parent directories", func(t *testing.T) {
		dir := t.TempDir()
		// Don't pre-create .metamorph — WriteState should handle it.
		state := &State{Status: "running", ProjectName: "proj"}

		if err := WriteState(dir, state); err != nil {
			t.Fatalf("WriteState: %v", err)
		}

		statePath := filepath.Join(dir, constants.StateFile)
		if _, err := os.Stat(statePath); err != nil {
			t.Errorf("state file not created: %v", err)
		}
	})

	t.Run("atomic write does not corrupt on overwrite", func(t *testing.T) {
		dir := t.TempDir()

		// Write initial state.
		state1 := &State{Status: "running", ProjectName: "v1"}
		if err := WriteState(dir, state1); err != nil {
			t.Fatalf("WriteState v1: %v", err)
		}

		// Overwrite with new state.
		state2 := &State{Status: "stopped", ProjectName: "v2"}
		if err := WriteState(dir, state2); err != nil {
			t.Fatalf("WriteState v2: %v", err)
		}

		// Read back.
		data, _ := os.ReadFile(filepath.Join(dir, constants.StateFile))
		var got State
		_ = json.Unmarshal(data, &got)
		if got.Status != "stopped" || got.ProjectName != "v2" {
			t.Errorf("got Status=%q ProjectName=%q, want stopped/v2", got.Status, got.ProjectName)
		}
	})

	t.Run("no temp file left behind", func(t *testing.T) {
		dir := t.TempDir()
		state := &State{Status: "running"}
		_ = WriteState(dir, state)

		tmpPath := filepath.Join(dir, constants.StateFile+".tmp")
		if _, err := os.Stat(tmpPath); err == nil {
			t.Error("temp file should be removed after rename")
		}
	})
}

func TestStateJSONRoundTrip(t *testing.T) {
	taskName := "fix-tests"
	original := &State{
		Status:      "running",
		StartedAt:   time.Date(2025, 3, 10, 8, 30, 0, 0, time.UTC),
		ProjectName: "metamorph",
		Agents: []AgentState{
			{
				ID:                1,
				Role:              "developer",
				ContainerID:       "cid-111",
				Status:            "running",
				SessionsCompleted: 5,
				LastActivity:      time.Date(2025, 3, 10, 9, 0, 0, 0, time.UTC),
				CurrentTask:       &taskName,
			},
			{
				ID:          2,
				Role:        "tester",
				ContainerID: "cid-222",
				Status:      "exited",
				CurrentTask: nil,
			},
		},
		Stats: Stats{
			TotalCommits:   100,
			TotalSessions:  25,
			TasksCompleted: 12,
			UptimeSeconds:  7200,
		},
	}

	data, err := json.MarshalIndent(original, "", "  ")
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded State
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.Status != original.Status {
		t.Errorf("Status = %q, want %q", decoded.Status, original.Status)
	}
	if !decoded.StartedAt.Equal(original.StartedAt) {
		t.Errorf("StartedAt = %v, want %v", decoded.StartedAt, original.StartedAt)
	}
	if len(decoded.Agents) != 2 {
		t.Fatalf("len(Agents) = %d, want 2", len(decoded.Agents))
	}
	if decoded.Agents[0].CurrentTask == nil || *decoded.Agents[0].CurrentTask != taskName {
		t.Errorf("Agent[0].CurrentTask = %v", decoded.Agents[0].CurrentTask)
	}
	if decoded.Agents[1].CurrentTask != nil {
		t.Errorf("Agent[1].CurrentTask = %v, want nil", decoded.Agents[1].CurrentTask)
	}
	if decoded.Stats != original.Stats {
		t.Errorf("Stats = %+v, want %+v", decoded.Stats, original.Stats)
	}
}

// --- PID Management Tests ---

func TestReadPID(t *testing.T) {
	t.Run("reads valid PID", func(t *testing.T) {
		dir := t.TempDir()
		pidPath := filepath.Join(dir, "test.pid")
		_ = os.WriteFile(pidPath, []byte("12345"), 0644)

		pid, err := readPID(pidPath)
		if err != nil {
			t.Fatalf("readPID: %v", err)
		}
		if pid != 12345 {
			t.Errorf("pid = %d, want 12345", pid)
		}
	})

	t.Run("handles whitespace and newline", func(t *testing.T) {
		dir := t.TempDir()
		pidPath := filepath.Join(dir, "test.pid")
		_ = os.WriteFile(pidPath, []byte("  67890\n"), 0644)

		pid, err := readPID(pidPath)
		if err != nil {
			t.Fatalf("readPID: %v", err)
		}
		if pid != 67890 {
			t.Errorf("pid = %d, want 67890", pid)
		}
	})

	t.Run("returns error for missing file", func(t *testing.T) {
		_, err := readPID("/nonexistent/path/test.pid")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "failed to read pid file") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("returns error for invalid content", func(t *testing.T) {
		dir := t.TempDir()
		pidPath := filepath.Join(dir, "test.pid")
		_ = os.WriteFile(pidPath, []byte("not-a-number"), 0644)

		_, err := readPID(pidPath)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "invalid pid") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("returns error for empty file", func(t *testing.T) {
		dir := t.TempDir()
		pidPath := filepath.Join(dir, "test.pid")
		_ = os.WriteFile(pidPath, []byte(""), 0644)

		_, err := readPID(pidPath)
		if err == nil {
			t.Fatal("expected error for empty file")
		}
	})
}

func TestProcessAlive(t *testing.T) {
	t.Run("current process is alive", func(t *testing.T) {
		// Our own PID should always be alive.
		if !processAlive(os.Getpid()) {
			t.Error("current process should be alive")
		}
	})

	t.Run("non-existent PID is not alive", func(t *testing.T) {
		// PID 999999 is very unlikely to exist.
		if processAlive(999999) {
			t.Skip("PID 999999 unexpectedly exists")
		}
	})
}

func TestIsRunning(t *testing.T) {
	t.Run("returns true when PID file has live process", func(t *testing.T) {
		dir := t.TempDir()
		pidDir := filepath.Join(dir, ".metamorph")
		_ = os.MkdirAll(pidDir, 0755)

		pidPath := filepath.Join(dir, constants.DaemonPIDFile)
		_ = os.MkdirAll(filepath.Dir(pidPath), 0755)
		_ = os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0644)

		if !IsRunning(dir) {
			t.Error("expected IsRunning = true for current process PID")
		}
	})

	t.Run("returns false when PID file missing", func(t *testing.T) {
		dir := t.TempDir()
		if IsRunning(dir) {
			t.Error("expected IsRunning = false when no PID file")
		}
	})

	t.Run("returns false when PID is stale", func(t *testing.T) {
		dir := t.TempDir()
		pidPath := filepath.Join(dir, constants.DaemonPIDFile)
		_ = os.MkdirAll(filepath.Dir(pidPath), 0755)
		// Use a PID that almost certainly doesn't exist.
		_ = os.WriteFile(pidPath, []byte("999999"), 0644)

		if IsRunning(dir) {
			t.Skip("PID 999999 unexpectedly exists")
		}
	})
}

// --- GetStatus Tests ---

func TestGetStatus(t *testing.T) {
	t.Run("reads state and reports running when alive", func(t *testing.T) {
		dir := t.TempDir()

		// Write state file.
		state := &State{
			Status:      "running",
			ProjectName: "proj",
			Agents: []AgentState{
				{ID: 1, Role: "developer", Status: "running"},
			},
		}
		_ = WriteState(dir, state)

		// Write PID file with our own PID (so it looks alive).
		pidPath := filepath.Join(dir, constants.DaemonPIDFile)
		_ = os.MkdirAll(filepath.Dir(pidPath), 0755)
		_ = os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0644)

		got, err := GetStatus(dir)
		if err != nil {
			t.Fatalf("GetStatus: %v", err)
		}
		if got.Status != "running" {
			t.Errorf("Status = %q, want running", got.Status)
		}
		if got.ProjectName != "proj" {
			t.Errorf("ProjectName = %q", got.ProjectName)
		}
	})

	t.Run("marks stopped when PID is stale", func(t *testing.T) {
		dir := t.TempDir()

		// Write state saying "running".
		state := &State{Status: "running", ProjectName: "proj"}
		_ = WriteState(dir, state)

		// Write stale PID file.
		pidPath := filepath.Join(dir, constants.DaemonPIDFile)
		_ = os.MkdirAll(filepath.Dir(pidPath), 0755)
		_ = os.WriteFile(pidPath, []byte("999999"), 0644)

		got, err := GetStatus(dir)
		if err != nil {
			t.Fatalf("GetStatus: %v", err)
		}
		if got.Status != "stopped" {
			t.Errorf("Status = %q, want stopped (stale PID)", got.Status)
		}
	})

	t.Run("returns error when state file missing", func(t *testing.T) {
		dir := t.TempDir()

		_, err := GetStatus(dir)
		if err == nil {
			t.Fatal("expected error when state file missing")
		}
		if !strings.Contains(err.Error(), "failed to read state") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("returns error for corrupt JSON", func(t *testing.T) {
		dir := t.TempDir()
		statePath := filepath.Join(dir, constants.StateFile)
		_ = os.MkdirAll(filepath.Dir(statePath), 0755)
		_ = os.WriteFile(statePath, []byte("{invalid json"), 0644)

		_, err := GetStatus(dir)
		if err == nil {
			t.Fatal("expected error for corrupt JSON")
		}
		if !strings.Contains(err.Error(), "failed to parse state") {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

// --- Orphan Detection Tests ---

func TestCleanOrphansWithClient(t *testing.T) {
	t.Run("stops orphan containers", func(t *testing.T) {
		dir := t.TempDir()
		// No PID file → daemon is not running.

		mock := &mockDockerClient{
			listResult: []docker.AgentInfo{
				{ID: 1, ContainerID: "orphan-1", Status: "Up 2 hours"},
				{ID: 2, ContainerID: "orphan-2", Status: "Exited (0) 5 minutes ago"},
			},
		}

		if err := CleanOrphansWithClient(dir, mock); err != nil {
			t.Fatalf("CleanOrphansWithClient: %v", err)
		}

		if !mock.stopAllCall {
			t.Error("expected StopAllAgents to be called for orphan containers")
		}
	})

	t.Run("no-op when no orphans", func(t *testing.T) {
		dir := t.TempDir()

		mock := &mockDockerClient{
			listResult: []docker.AgentInfo{},
		}

		if err := CleanOrphansWithClient(dir, mock); err != nil {
			t.Fatalf("CleanOrphansWithClient: %v", err)
		}

		if mock.stopAllCall {
			t.Error("StopAllAgents should not be called when no orphans")
		}
	})

	t.Run("returns error when daemon already running", func(t *testing.T) {
		dir := t.TempDir()

		// Write PID file with our own PID.
		pidPath := filepath.Join(dir, constants.DaemonPIDFile)
		_ = os.MkdirAll(filepath.Dir(pidPath), 0755)
		_ = os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0644)

		mock := &mockDockerClient{}

		err := CleanOrphansWithClient(dir, mock)
		if err == nil {
			t.Fatal("expected error when daemon already running")
		}
		if !strings.Contains(err.Error(), "already running") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("ignores list errors gracefully", func(t *testing.T) {
		dir := t.TempDir()

		mock := &mockDockerClient{
			listErr: io.ErrUnexpectedEOF,
		}

		// Should not return an error — just silently fail.
		if err := CleanOrphansWithClient(dir, mock); err != nil {
			t.Fatalf("expected nil error, got: %v", err)
		}
	})
}

// --- normalizeStatus Tests ---

func TestNormalizeStatus(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Up 2 hours", "running"},
		{"Up About a minute", "running"},
		{"Exited (0) 5 minutes ago", "exited"},
		{"Exited (1) 30 seconds ago", "exited"},
		{"Created", "created"},
		{"created", "created"},
		{"Paused", "unknown"},
		{"", "unknown"},
	}

	for _, tt := range tests {
		got := normalizeStatus(tt.input)
		if got != tt.want {
			t.Errorf("normalizeStatus(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// --- updateAgentStates Tests ---

func TestUpdateAgentStates(t *testing.T) {
	t.Run("syncs container status into agent state", func(t *testing.T) {
		d := &Daemon{
			state: &State{
				Agents: []AgentState{
					{ID: 1, ContainerID: "old-cid-1", Status: "running"},
					{ID: 2, ContainerID: "old-cid-2", Status: "running"},
					{ID: 3, ContainerID: "old-cid-3", Status: "running"},
				},
			},
		}

		infos := []docker.AgentInfo{
			{ID: 1, ContainerID: "new-cid-1", Status: "Up 1 hour"},
			{ID: 3, ContainerID: "new-cid-3", Status: "Exited (0) 5 minutes ago"},
			// Agent 2 is missing → should become "stopped".
		}

		d.updateAgentStates(infos)

		a1 := d.state.Agents[0]
		if a1.ContainerID != "new-cid-1" {
			t.Errorf("Agent 1 ContainerID = %q", a1.ContainerID)
		}
		if a1.Status != "running" {
			t.Errorf("Agent 1 Status = %q, want running", a1.Status)
		}

		a2 := d.state.Agents[1]
		if a2.Status != "stopped" {
			t.Errorf("Agent 2 Status = %q, want stopped (missing from Docker)", a2.Status)
		}

		a3 := d.state.Agents[2]
		if a3.ContainerID != "new-cid-3" {
			t.Errorf("Agent 3 ContainerID = %q", a3.ContainerID)
		}
		if a3.Status != "exited" {
			t.Errorf("Agent 3 Status = %q, want exited", a3.Status)
		}
	})
}

// --- startAgents Tests ---

func TestStartAgents(t *testing.T) {
	t.Run("starts configured number of agents", func(t *testing.T) {
		mock := &mockDockerClient{
			startAgents: make(map[int]string),
		}

		d := &Daemon{
			projectDir: t.TempDir(),
			cfg: &config.Config{
				Agents: config.AgentsConfig{
					Count: 3,
					Roles: []string{"developer", "tester"},
					Model: "claude-sonnet",
				},
			},
			apiKey: "sk-test",
			docker: mock,
		}

		agents, err := d.startAgents(context.Background())
		if err != nil {
			t.Fatalf("startAgents: %v", err)
		}

		if len(agents) != 3 {
			t.Fatalf("expected 3 agents, got %d", len(agents))
		}

		// Verify role assignment (round-robin).
		if agents[0].Role != "developer" {
			t.Errorf("Agent 1 Role = %q, want developer", agents[0].Role)
		}
		if agents[1].Role != "tester" {
			t.Errorf("Agent 2 Role = %q, want tester", agents[1].Role)
		}
		if agents[2].Role != "developer" {
			t.Errorf("Agent 3 Role = %q, want developer (round-robin)", agents[2].Role)
		}

		// Verify IDs.
		for i, a := range agents {
			if a.ID != i+1 {
				t.Errorf("Agent[%d].ID = %d, want %d", i, a.ID, i+1)
			}
			if a.Status != "running" {
				t.Errorf("Agent[%d].Status = %q, want running", i, a.Status)
			}
		}
	})

	t.Run("defaults to developer role when no roles configured", func(t *testing.T) {
		mock := &mockDockerClient{
			startAgents: make(map[int]string),
		}

		d := &Daemon{
			projectDir: t.TempDir(),
			cfg: &config.Config{
				Agents: config.AgentsConfig{
					Count: 1,
					Roles: nil,
					Model: "claude-sonnet",
				},
			},
			apiKey: "sk-test",
			docker: mock,
		}

		agents, err := d.startAgents(context.Background())
		if err != nil {
			t.Fatalf("startAgents: %v", err)
		}

		if agents[0].Role != "developer" {
			t.Errorf("Role = %q, want developer", agents[0].Role)
		}
	})

	t.Run("returns error when docker fails", func(t *testing.T) {
		mock := &mockDockerClient{
			startErr: io.ErrClosedPipe,
		}

		d := &Daemon{
			projectDir: t.TempDir(),
			cfg: &config.Config{
				Agents: config.AgentsConfig{Count: 1, Model: "claude-sonnet"},
			},
			apiKey: "sk-test",
			docker: mock,
		}

		_, err := d.startAgents(context.Background())
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "failed to start agent") {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

// --- Monitor Panic Recovery Tests ---

// panicDockerClient is a mock that panics on ListAgents to test recovery.
type panicDockerClient struct {
	mockDockerClient
}

func (p *panicDockerClient) ListAgents(ctx context.Context) ([]docker.AgentInfo, error) {
	panic("simulated panic in ListAgents")
}

func TestMonitorRecoversPanic(t *testing.T) {
	dir := t.TempDir()

	d := &Daemon{
		projectDir:        dir,
		docker:            &panicDockerClient{},
		startedAt:         time.Now().UTC(),
		lastErrorNotified: make(map[int]time.Time),
		cfg: &config.Config{
			Project: config.ProjectConfig{Name: "test"},
		},
		state: &State{
			Status: "running",
			Agents: []AgentState{
				{ID: 1, Role: "developer", Status: "running"},
			},
		},
	}

	// monitor() should recover from panic and not crash.
	d.monitor(context.Background())

	// If we get here, the panic was recovered.
	if d.state.Status != "running" {
		t.Errorf("Status = %q, want running (panic should be recovered)", d.state.Status)
	}
}

// --- restartCrashedAgents Tests ---

func TestRestartCrashedAgents(t *testing.T) {
	t.Run("restarts stopped agents", func(t *testing.T) {
		mock := &mockDockerClient{
			startAgents: make(map[int]string),
		}

		d := &Daemon{
			projectDir:        t.TempDir(),
			docker:            mock,
			lastErrorNotified: make(map[int]time.Time),
			cfg: &config.Config{
				Project: config.ProjectConfig{Name: "test"},
				Agents:  config.AgentsConfig{Model: "claude-sonnet"},
			},
			state: &State{
				Agents: []AgentState{
					{ID: 1, Role: "developer", Status: "running"},
					{ID: 2, Role: "tester", Status: "running"},
				},
			},
		}

		// Only agent 1 is reported as running; agent 2 has crashed.
		infos := []docker.AgentInfo{
			{ID: 1, Status: "Up 1 hour"},
		}

		d.restartCrashedAgents(context.Background(), infos)

		// Agent 2 should have been restarted.
		if len(mock.stopCalls) == 0 {
			t.Error("expected StopAgent to be called for crashed agent")
		}
		if _, ok := mock.startAgents[2]; !ok {
			t.Error("expected StartAgent to be called for agent-2")
		}
	})
}

// --- countCommitsAndNotify Tests ---

func TestCountCommitsAndNotify(t *testing.T) {
	// This test requires a git repo to count commits. Create one.
	dir := t.TempDir()
	upstreamPath := filepath.Join(dir, ".metamorph", "upstream.git")
	_ = os.MkdirAll(upstreamPath, 0755)

	// Init a non-bare repo with a commit so rev-list works.
	gitInit := func(d string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = d
		_ = cmd.Run()
	}
	gitInit(upstreamPath, "init")
	gitInit(upstreamPath, "config", "user.name", "test")
	gitInit(upstreamPath, "config", "user.email", "test@test")
	_ = os.WriteFile(filepath.Join(upstreamPath, "f.txt"), []byte("data"), 0644)
	gitInit(upstreamPath, "add", ".")
	gitInit(upstreamPath, "commit", "-m", "c1")

	d := &Daemon{
		projectDir:        dir,
		lastErrorNotified: make(map[int]time.Time),
		cfg: &config.Config{
			Project: config.ProjectConfig{Name: "test"},
		},
		state: &State{},
	}

	d.countCommitsAndNotify(time.Now().UTC())

	if d.state.Stats.TotalCommits != 1 {
		t.Errorf("TotalCommits = %d, want 1", d.state.Stats.TotalCommits)
	}
	if d.prevCommitCount != 1 {
		t.Errorf("prevCommitCount = %d, want 1", d.prevCommitCount)
	}
}

// --- checkAgentLogs Tests ---

func TestCheckAgentLogs(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, "agent_logs", "agent-1")
	_ = os.MkdirAll(logDir, 0755)

	// Write a log file with an ERROR line.
	_ = os.WriteFile(filepath.Join(logDir, "session-1.log"), []byte("line1\nERROR: test failed\nline3\n"), 0644)

	d := &Daemon{
		projectDir:        dir,
		lastErrorNotified: make(map[int]time.Time),
		cfg: &config.Config{
			Project:       config.ProjectConfig{Name: "test"},
			Notifications: config.NotificationsConfig{WebhookURL: ""},
		},
		state: &State{
			Agents: []AgentState{
				{ID: 1, Role: "developer"},
			},
		},
	}

	d.checkAgentLogs(time.Now().UTC())

	// Should have recorded the notification time (debounce).
	if _, ok := d.lastErrorNotified[1]; !ok {
		t.Error("expected lastErrorNotified to be set for agent 1")
	}
}

func TestCheckAgentLogsDebounce(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, "agent_logs", "agent-1")
	_ = os.MkdirAll(logDir, 0755)
	_ = os.WriteFile(filepath.Join(logDir, "session-1.log"), []byte("ERROR: something\n"), 0644)

	d := &Daemon{
		projectDir: dir,
		lastErrorNotified: map[int]time.Time{
			1: time.Now().UTC(), // Already notified just now.
		},
		cfg: &config.Config{
			Project: config.ProjectConfig{Name: "test"},
		},
		state: &State{
			Agents: []AgentState{
				{ID: 1, Role: "developer"},
			},
		},
	}

	before := d.lastErrorNotified[1]
	d.checkAgentLogs(time.Now().UTC())

	// Should not have updated the notification time (debounced).
	if !d.lastErrorNotified[1].Equal(before) {
		t.Error("expected lastErrorNotified to remain unchanged (debounced)")
	}
}

// --- flushCommitBatch Tests ---

func TestFlushCommitBatch(t *testing.T) {
	t.Run("no-op when no pending commits", func(t *testing.T) {
		d := &Daemon{
			pendingCommits: nil,
			cfg: &config.Config{
				Project: config.ProjectConfig{Name: "test"},
			},
		}
		d.flushCommitBatch(time.Now().UTC())
		// Should not panic.
	})

	t.Run("does not flush before window elapses", func(t *testing.T) {
		d := &Daemon{
			pendingCommits:   []string{"commit1"},
			commitBatchStart: time.Now().UTC(),
			cfg: &config.Config{
				Project:       config.ProjectConfig{Name: "test"},
				Notifications: config.NotificationsConfig{WebhookURL: ""},
			},
		}
		d.flushCommitBatch(time.Now().UTC())
		if d.pendingCommits == nil {
			t.Error("should not have flushed yet")
		}
	})

	t.Run("flushes after window elapses", func(t *testing.T) {
		d := &Daemon{
			pendingCommits:   []string{"commit1", "commit2"},
			commitBatchStart: time.Now().UTC().Add(-2 * time.Minute),
			cfg: &config.Config{
				Project:       config.ProjectConfig{Name: "test"},
				Notifications: config.NotificationsConfig{WebhookURL: ""},
			},
		}
		d.flushCommitBatch(time.Now().UTC())
		if d.pendingCommits != nil {
			t.Error("expected pending commits to be cleared after flush")
		}
	})
}

// --- sendEvent Tests ---

func TestSendEvent(t *testing.T) {
	t.Run("no-op when webhook URL is empty", func(t *testing.T) {
		d := &Daemon{
			cfg: &config.Config{
				Notifications: config.NotificationsConfig{WebhookURL: ""},
			},
		}
		// Should not panic.
		d.sendEvent(notify.Event{Type: notify.EventAgentCrashed})
	})
}

// --- shutdown Tests ---

func TestShutdown(t *testing.T) {
	t.Run("stops agents and writes final state", func(t *testing.T) {
		dir := t.TempDir()
		mock := &mockDockerClient{}

		d := &Daemon{
			projectDir: dir,
			docker:     mock,
			startedAt:  time.Now().Add(-time.Hour),
			state: &State{
				Status: "running",
				Agents: []AgentState{
					{ID: 1, Status: "running"},
				},
			},
		}

		// Write a PID file to verify it gets removed.
		pidPath := filepath.Join(dir, constants.DaemonPIDFile)
		_ = os.MkdirAll(filepath.Dir(pidPath), 0755)
		_ = os.WriteFile(pidPath, []byte("12345"), 0644)

		if err := d.shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown: %v", err)
		}

		if !mock.stopAllCall {
			t.Error("expected StopAllAgents to be called")
		}

		if d.state.Status != "stopped" {
			t.Errorf("Status = %q, want stopped", d.state.Status)
		}

		if d.state.Stats.UptimeSeconds < 3600 {
			t.Errorf("UptimeSeconds = %d, expected >= 3600", d.state.Stats.UptimeSeconds)
		}

		// PID file should be removed.
		if _, err := os.Stat(pidPath); err == nil {
			t.Error("PID file should be removed after shutdown")
		}

		// State file should exist with stopped status.
		statePath := filepath.Join(dir, constants.StateFile)
		data, err := os.ReadFile(statePath)
		if err != nil {
			t.Fatalf("state file not written: %v", err)
		}
		var got State
		_ = json.Unmarshal(data, &got)
		if got.Status != "stopped" {
			t.Errorf("persisted Status = %q, want stopped", got.Status)
		}
	})
}
