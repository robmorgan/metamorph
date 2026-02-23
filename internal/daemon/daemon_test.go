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

func (m *mockDockerClient) BuildImage(projectDir string, extraPackages []string) error {
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

// setupTestHome overrides HOME to a temp dir for tests that rely on global path resolution.
func setupTestHome(t *testing.T) string {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	return dir
}

// --- State Serialization Tests ---

func TestWriteStateToPath(t *testing.T) {
	t.Run("writes valid JSON", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "state.json")

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

		if err := writeStateToPath(path, state); err != nil {
			t.Fatalf("writeStateToPath: %v", err)
		}

		data, err := os.ReadFile(path)
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
		path := filepath.Join(dir, "sub", "dir", "state.json")

		state := &State{Status: "running", ProjectName: "proj"}
		if err := writeStateToPath(path, state); err != nil {
			t.Fatalf("writeStateToPath: %v", err)
		}

		if _, err := os.Stat(path); err != nil {
			t.Errorf("state file not created: %v", err)
		}
	})

	t.Run("atomic write does not corrupt on overwrite", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "state.json")

		state1 := &State{Status: "running", ProjectName: "v1"}
		if err := writeStateToPath(path, state1); err != nil {
			t.Fatalf("writeStateToPath v1: %v", err)
		}

		state2 := &State{Status: "stopped", ProjectName: "v2"}
		if err := writeStateToPath(path, state2); err != nil {
			t.Fatalf("writeStateToPath v2: %v", err)
		}

		data, _ := os.ReadFile(path)
		var got State
		_ = json.Unmarshal(data, &got)
		if got.Status != "stopped" || got.ProjectName != "v2" {
			t.Errorf("got Status=%q ProjectName=%q, want stopped/v2", got.Status, got.ProjectName)
		}
	})

	t.Run("no temp file left behind", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "state.json")

		_ = writeStateToPath(path, &State{Status: "running"})

		tmpPath := path + ".tmp"
		if _, err := os.Stat(tmpPath); err == nil {
			t.Error("temp file should be removed after rename")
		}
	})
}

func TestWriteState(t *testing.T) {
	home := setupTestHome(t)

	state := &State{Status: "running", ProjectName: "my-proj"}
	if err := WriteState("my-proj", state); err != nil {
		t.Fatalf("WriteState: %v", err)
	}

	expected := filepath.Join(home, ".metamorph", "projects", "my-proj", "state.json")
	data, err := os.ReadFile(expected)
	if err != nil {
		t.Fatalf("state file not at expected path %s: %v", expected, err)
	}

	var got State
	_ = json.Unmarshal(data, &got)
	if got.ProjectName != "my-proj" {
		t.Errorf("ProjectName = %q, want my-proj", got.ProjectName)
	}
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
		if !processAlive(os.Getpid()) {
			t.Error("current process should be alive")
		}
	})

	t.Run("non-existent PID is not alive", func(t *testing.T) {
		if processAlive(999999) {
			t.Skip("PID 999999 unexpectedly exists")
		}
	})
}

func TestIsRunning(t *testing.T) {
	t.Run("returns true when PID file has live process", func(t *testing.T) {
		home := setupTestHome(t)
		globalDir := filepath.Join(home, ".metamorph")
		_ = os.MkdirAll(globalDir, 0755)

		pidPath := filepath.Join(globalDir, constants.DaemonPIDFile)
		_ = os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0644)

		if !IsRunning() {
			t.Error("expected IsRunning = true for current process PID")
		}
	})

	t.Run("returns false when PID file missing", func(t *testing.T) {
		setupTestHome(t)

		if IsRunning() {
			t.Error("expected IsRunning = false when no PID file")
		}
	})

	t.Run("returns false when PID is stale", func(t *testing.T) {
		home := setupTestHome(t)
		globalDir := filepath.Join(home, ".metamorph")
		_ = os.MkdirAll(globalDir, 0755)

		pidPath := filepath.Join(globalDir, constants.DaemonPIDFile)
		_ = os.WriteFile(pidPath, []byte("999999"), 0644)

		if IsRunning() {
			t.Skip("PID 999999 unexpectedly exists")
		}
	})
}

// --- GetStatus Tests ---

func TestGetStatus(t *testing.T) {
	t.Run("reads state and reports running when alive", func(t *testing.T) {
		home := setupTestHome(t)

		state := &State{
			Status:      "running",
			ProjectName: "proj",
			Agents: []AgentState{
				{ID: 1, Role: "developer", Status: "running"},
			},
		}
		if err := WriteState("proj", state); err != nil {
			t.Fatalf("WriteState: %v", err)
		}

		// Write PID file with our own PID (so it looks alive).
		globalDir := filepath.Join(home, ".metamorph")
		pidPath := filepath.Join(globalDir, constants.DaemonPIDFile)
		_ = os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0644)

		got, err := GetStatus("proj")
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
		home := setupTestHome(t)

		state := &State{
			Status:      "running",
			ProjectName: "proj",
			Agents: []AgentState{
				{ID: 1, Role: "developer", Status: "running"},
				{ID: 2, Role: "tester", Status: "running"},
				{ID: 3, Role: "developer", Status: "error"},
			},
		}
		if err := WriteState("proj", state); err != nil {
			t.Fatalf("WriteState: %v", err)
		}

		// Write stale PID file.
		globalDir := filepath.Join(home, ".metamorph")
		_ = os.MkdirAll(globalDir, 0755)
		pidPath := filepath.Join(globalDir, constants.DaemonPIDFile)
		_ = os.WriteFile(pidPath, []byte("999999"), 0644)

		got, err := GetStatus("proj")
		if err != nil {
			t.Fatalf("GetStatus: %v", err)
		}
		if got.Status != "stopped" {
			t.Errorf("Status = %q, want stopped (stale PID)", got.Status)
		}
		for _, a := range got.Agents {
			if a.Status == "running" {
				t.Errorf("agent-%d Status = %q, want stopped when daemon is dead", a.ID, a.Status)
			}
		}
		if got.Agents[2].Status != "error" {
			t.Errorf("agent-3 Status = %q, want error (should be preserved)", got.Agents[2].Status)
		}
	})

	t.Run("returns error when state file missing", func(t *testing.T) {
		setupTestHome(t)

		_, err := GetStatus("nonexistent")
		if err == nil {
			t.Fatal("expected error when state file missing")
		}
		if !strings.Contains(err.Error(), "failed to read state") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("returns error for corrupt JSON", func(t *testing.T) {
		home := setupTestHome(t)

		statePath := filepath.Join(home, ".metamorph", "projects", "bad", constants.ProjectStateFile)
		_ = os.MkdirAll(filepath.Dir(statePath), 0755)
		_ = os.WriteFile(statePath, []byte("{invalid json"), 0644)

		_, err := GetStatus("bad")
		if err == nil {
			t.Fatal("expected error for corrupt JSON")
		}
		if !strings.Contains(err.Error(), "failed to parse state") {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

// --- readDaemonLogHint Tests ---

func TestReadDaemonLogHint(t *testing.T) {
	t.Run("returns formatted hint when file has content", func(t *testing.T) {
		dir := t.TempDir()
		logPath := filepath.Join(dir, "daemon.log")
		_ = os.WriteFile(logPath, []byte("error: build failed\nexit status 1\n"), 0644)

		hint := readDaemonLogHint(logPath)
		if hint == "" {
			t.Fatal("expected non-empty hint")
		}
		if !strings.Contains(hint, "Daemon log") {
			t.Errorf("hint missing 'Daemon log' prefix: %q", hint)
		}
		if !strings.Contains(hint, "error: build failed") {
			t.Errorf("hint missing log content: %q", hint)
		}
		if !strings.Contains(hint, logPath) {
			t.Errorf("hint missing log path: %q", hint)
		}
	})

	t.Run("returns empty string for missing file", func(t *testing.T) {
		hint := readDaemonLogHint("/nonexistent/path/daemon.log")
		if hint != "" {
			t.Errorf("expected empty string, got: %q", hint)
		}
	})

	t.Run("returns empty string for empty file", func(t *testing.T) {
		dir := t.TempDir()
		logPath := filepath.Join(dir, "daemon.log")
		_ = os.WriteFile(logPath, []byte(""), 0644)

		hint := readDaemonLogHint(logPath)
		if hint != "" {
			t.Errorf("expected empty string for empty file, got: %q", hint)
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
		d := &Daemon{}

		p := &ProjectInstance{
			Agents: []AgentState{
				{ID: 1, ContainerID: "old-cid-1", Status: "running"},
				{ID: 2, ContainerID: "old-cid-2", Status: "running"},
				{ID: 3, ContainerID: "old-cid-3", Status: "running"},
			},
		}

		infos := []docker.AgentInfo{
			{ID: 1, ContainerID: "new-cid-1", Status: "Up 1 hour"},
			{ID: 3, ContainerID: "new-cid-3", Status: "Exited (0) 5 minutes ago"},
			// Agent 2 is missing → should become "stopped".
		}

		d.updateAgentStates(p, infos)

		a1 := p.Agents[0]
		if a1.ContainerID != "new-cid-1" {
			t.Errorf("Agent 1 ContainerID = %q", a1.ContainerID)
		}
		if a1.Status != "running" {
			t.Errorf("Agent 1 Status = %q, want running", a1.Status)
		}

		a2 := p.Agents[1]
		if a2.Status != "stopped" {
			t.Errorf("Agent 2 Status = %q, want stopped (missing from Docker)", a2.Status)
		}

		a3 := p.Agents[2]
		if a3.ContainerID != "new-cid-3" {
			t.Errorf("Agent 3 ContainerID = %q", a3.ContainerID)
		}
		if a3.Status != "exited" {
			t.Errorf("Agent 3 Status = %q, want exited", a3.Status)
		}
	})
}

// --- ProjectInstance.StartAgents Tests ---

func TestStartAgents(t *testing.T) {
	t.Run("starts configured number of agents", func(t *testing.T) {
		mock := &mockDockerClient{
			startAgents: make(map[int]string),
		}

		p := &ProjectInstance{
			Name: "test",
			Config: &config.Config{
				Agents: config.AgentsConfig{
					Count: 3,
					Roles: []string{"developer", "tester"},
					Model: "claude-sonnet",
				},
				Broker: config.BrokerConfig{Port: 8377},
			},
			docker: mock,
			apiKey: "sk-test",
		}

		if err := p.StartAgents(context.Background()); err != nil {
			t.Fatalf("StartAgents: %v", err)
		}

		if len(p.Agents) != 3 {
			t.Fatalf("expected 3 agents, got %d", len(p.Agents))
		}

		// Verify role assignment (round-robin).
		if p.Agents[0].Role != "developer" {
			t.Errorf("Agent 1 Role = %q, want developer", p.Agents[0].Role)
		}
		if p.Agents[1].Role != "tester" {
			t.Errorf("Agent 2 Role = %q, want tester", p.Agents[1].Role)
		}
		if p.Agents[2].Role != "developer" {
			t.Errorf("Agent 3 Role = %q, want developer (round-robin)", p.Agents[2].Role)
		}

		// Verify IDs.
		for i, a := range p.Agents {
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

		p := &ProjectInstance{
			Name: "test",
			Config: &config.Config{
				Agents: config.AgentsConfig{
					Count: 1,
					Roles: nil,
					Model: "claude-sonnet",
				},
				Broker: config.BrokerConfig{Port: 8377},
			},
			docker: mock,
			apiKey: "sk-test",
		}

		if err := p.StartAgents(context.Background()); err != nil {
			t.Fatalf("StartAgents: %v", err)
		}

		if p.Agents[0].Role != "developer" {
			t.Errorf("Role = %q, want developer", p.Agents[0].Role)
		}
	})

	t.Run("returns error when docker fails", func(t *testing.T) {
		mock := &mockDockerClient{
			startErr: io.ErrClosedPipe,
		}

		p := &ProjectInstance{
			Name: "test",
			Config: &config.Config{
				Agents: config.AgentsConfig{Count: 1, Model: "claude-sonnet"},
				Broker: config.BrokerConfig{Port: 8377},
			},
			docker: mock,
			apiKey: "sk-test",
		}

		err := p.StartAgents(context.Background())
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

	proj := &ProjectInstance{
		Name:   "test",
		docker: &panicDockerClient{},
		Config: &config.Config{
			Project: config.ProjectConfig{Name: "test"},
		},
		Agents: []AgentState{
			{ID: 1, Role: "developer", Status: "running"},
		},
		Repos:             []config.RepoConfig{{Path: "/tmp/test"}},
		lastErrorNotified: make(map[int]time.Time),
		stateDir:          dir,
	}

	d := &Daemon{
		globalDir: dir,
		projects:  map[string]*ProjectInstance{"test": proj},
	}

	// monitor() should recover from panic and not crash.
	d.monitor(context.Background())

	// If we get here, the panic was recovered.
	if proj.Agents[0].Status != "running" {
		t.Errorf("Status = %q, want running (panic should be recovered)", proj.Agents[0].Status)
	}
}

// --- restartCrashedAgents Tests ---

func TestRestartCrashedAgents(t *testing.T) {
	t.Run("restarts stopped agents", func(t *testing.T) {
		mock := &mockDockerClient{
			startAgents: make(map[int]string),
		}

		p := &ProjectInstance{
			Name: "test",
			Config: &config.Config{
				Project: config.ProjectConfig{Name: "test"},
				Agents:  config.AgentsConfig{Model: "claude-sonnet"},
				Broker:  config.BrokerConfig{Port: 8377},
			},
			docker:            mock,
			apiKey:            "sk-test",
			lastErrorNotified: make(map[int]time.Time),
			Agents: []AgentState{
				{ID: 1, Role: "developer", Status: "running"},
				{ID: 2, Role: "tester", Status: "running"},
			},
		}

		d := &Daemon{}

		// Only agent 1 is reported as running; agent 2 has crashed.
		infos := []docker.AgentInfo{
			{ID: 1, Status: "Up 1 hour"},
		}

		d.restartCrashedAgents(context.Background(), p, infos)

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
	stateDir := t.TempDir()

	p := &ProjectInstance{
		Name: "test",
		Config: &config.Config{
			Project: config.ProjectConfig{Name: "test"},
		},
		stateDir:          stateDir,
		lastErrorNotified: make(map[int]time.Time),
	}

	upstreamPath := p.UpstreamRepoPath()
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

	d := &Daemon{}
	d.countCommitsAndNotify(p, time.Now().UTC())

	if p.prevCommitCount != 1 {
		t.Errorf("prevCommitCount = %d, want 1", p.prevCommitCount)
	}
}

// --- checkAgentLogs Tests ---

func TestCheckAgentLogs(t *testing.T) {
	stateDir := t.TempDir()
	logDir := filepath.Join(stateDir, constants.AgentLogDir, "agent-1")
	_ = os.MkdirAll(logDir, 0755)

	// Write a log file with an ERROR line.
	_ = os.WriteFile(filepath.Join(logDir, "session-1.log"), []byte("line1\nERROR: test failed\nline3\n"), 0644)

	p := &ProjectInstance{
		Name: "test",
		Config: &config.Config{
			Project:       config.ProjectConfig{Name: "test"},
			Notifications: config.NotificationsConfig{WebhookURL: ""},
		},
		stateDir:          stateDir,
		lastErrorNotified: make(map[int]time.Time),
		Agents: []AgentState{
			{ID: 1, Role: "developer"},
		},
	}

	p.CheckAgentLogs(time.Now().UTC())

	// Should have recorded the notification time (debounce).
	if _, ok := p.lastErrorNotified[1]; !ok {
		t.Error("expected lastErrorNotified to be set for agent 1")
	}
}

func TestCheckAgentLogsDebounce(t *testing.T) {
	stateDir := t.TempDir()
	logDir := filepath.Join(stateDir, constants.AgentLogDir, "agent-1")
	_ = os.MkdirAll(logDir, 0755)
	_ = os.WriteFile(filepath.Join(logDir, "session-1.log"), []byte("ERROR: something\n"), 0644)

	now := time.Now().UTC()
	p := &ProjectInstance{
		Name: "test",
		Config: &config.Config{
			Project: config.ProjectConfig{Name: "test"},
		},
		stateDir: stateDir,
		lastErrorNotified: map[int]time.Time{
			1: now, // Already notified just now.
		},
		Agents: []AgentState{
			{ID: 1, Role: "developer"},
		},
	}

	before := p.lastErrorNotified[1]
	p.CheckAgentLogs(now)

	// Should not have updated the notification time (debounced).
	if !p.lastErrorNotified[1].Equal(before) {
		t.Error("expected lastErrorNotified to remain unchanged (debounced)")
	}
}

// --- flushCommitBatch Tests ---

func TestFlushCommitBatch(t *testing.T) {
	t.Run("no-op when no pending commits", func(t *testing.T) {
		d := &Daemon{}
		p := &ProjectInstance{
			Name: "test",
			Config: &config.Config{
				Project: config.ProjectConfig{Name: "test"},
			},
			pendingCommits: nil,
		}
		d.flushCommitBatch(p, time.Now().UTC())
		// Should not panic.
	})

	t.Run("does not flush before window elapses", func(t *testing.T) {
		d := &Daemon{}
		p := &ProjectInstance{
			Name: "test",
			Config: &config.Config{
				Project:       config.ProjectConfig{Name: "test"},
				Notifications: config.NotificationsConfig{WebhookURL: ""},
			},
			pendingCommits:   []string{"commit1"},
			commitBatchStart: time.Now().UTC(),
		}
		d.flushCommitBatch(p, time.Now().UTC())
		if p.pendingCommits == nil {
			t.Error("should not have flushed yet")
		}
	})

	t.Run("flushes after window elapses", func(t *testing.T) {
		d := &Daemon{}
		p := &ProjectInstance{
			Name: "test",
			Config: &config.Config{
				Project:       config.ProjectConfig{Name: "test"},
				Notifications: config.NotificationsConfig{WebhookURL: ""},
			},
			pendingCommits:   []string{"commit1", "commit2"},
			commitBatchStart: time.Now().UTC().Add(-2 * time.Minute),
		}
		d.flushCommitBatch(p, time.Now().UTC())
		if p.pendingCommits != nil {
			t.Error("expected pending commits to be cleared after flush")
		}
	})
}

// --- SendEvent Tests ---

func TestSendEvent(t *testing.T) {
	t.Run("no-op when webhook URL is empty", func(t *testing.T) {
		p := &ProjectInstance{
			Name: "test",
			Config: &config.Config{
				Notifications: config.NotificationsConfig{WebhookURL: ""},
			},
		}
		// Should not panic.
		p.SendEvent(notify.Event{Type: notify.EventAgentCrashed})
	})
}

// --- shutdown Tests ---

func TestShutdown(t *testing.T) {
	t.Run("stops agents and removes PID file", func(t *testing.T) {
		dir := t.TempDir()
		mock := &mockDockerClient{}

		p := &ProjectInstance{
			Name:   "test",
			docker: mock,
			Agents: []AgentState{
				{ID: 1, Status: "running"},
			},
		}

		d := &Daemon{
			globalDir: dir,
			projects:  map[string]*ProjectInstance{"test": p},
		}

		// Write a PID file to verify it gets removed.
		pidPath := filepath.Join(dir, constants.DaemonPIDFile)
		_ = os.WriteFile(pidPath, []byte("12345"), 0644)

		if err := d.shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown: %v", err)
		}

		if !mock.stopAllCall {
			t.Error("expected StopAllAgents to be called")
		}

		// PID file should be removed.
		if _, err := os.Stat(pidPath); err == nil {
			t.Error("PID file should be removed after shutdown")
		}
	})
}
