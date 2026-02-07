package docker

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// dockerFrame creates a Docker multiplexed log frame (stdout stream type).
func dockerFrame(payload string) []byte {
	var buf bytes.Buffer
	// Stream type: 1=stdout, then 3 zero bytes, then 4-byte big-endian size.
	header := make([]byte, 8)
	header[0] = 1 // stdout
	binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))
	buf.Write(header)
	buf.WriteString(payload)
	return buf.Bytes()
}

// mockDocker implements the dockerAPI interface for testing.
type mockDocker struct {
	pingErr     error
	buildErr    error
	buildBody   string
	createResp  container.CreateResponse
	createErr   error
	startErr    error
	stopErr     error
	removeErr   error
	listResult  []types.Container
	listErr     error
	inspectResp types.ContainerJSON
	inspectErr  error
	logsBody    string
	logsErr     error

	// Track calls for assertions.
	created []mockCreateCall
	started []string
	stopped []string
	removed []string
}

type mockCreateCall struct {
	Name   string
	Config *container.Config
	Host   *container.HostConfig
}

func (m *mockDocker) Ping(ctx context.Context) (types.Ping, error) {
	return types.Ping{}, m.pingErr
}

func (m *mockDocker) ImageBuild(ctx context.Context, buildContext io.Reader, options types.ImageBuildOptions) (types.ImageBuildResponse, error) {
	if m.buildErr != nil {
		return types.ImageBuildResponse{}, m.buildErr
	}
	body := io.NopCloser(strings.NewReader(m.buildBody))
	return types.ImageBuildResponse{Body: body}, nil
}

func (m *mockDocker) ContainerCreate(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error) {
	m.created = append(m.created, mockCreateCall{Name: containerName, Config: config, Host: hostConfig})
	return m.createResp, m.createErr
}

func (m *mockDocker) ContainerStart(ctx context.Context, containerID string, options container.StartOptions) error {
	m.started = append(m.started, containerID)
	return m.startErr
}

func (m *mockDocker) ContainerStop(ctx context.Context, containerID string, options container.StopOptions) error {
	m.stopped = append(m.stopped, containerID)
	return m.stopErr
}

func (m *mockDocker) ContainerRemove(ctx context.Context, containerID string, options container.RemoveOptions) error {
	m.removed = append(m.removed, containerID)
	return m.removeErr
}

func (m *mockDocker) ContainerList(ctx context.Context, options container.ListOptions) ([]types.Container, error) {
	return m.listResult, m.listErr
}

func (m *mockDocker) ContainerInspect(ctx context.Context, containerID string) (types.ContainerJSON, error) {
	return m.inspectResp, m.inspectErr
}

func (m *mockDocker) ContainerLogs(ctx context.Context, ctr string, options container.LogsOptions) (io.ReadCloser, error) {
	if m.logsErr != nil {
		return nil, m.logsErr
	}
	return io.NopCloser(strings.NewReader(m.logsBody)), nil
}

func TestBuildImage(t *testing.T) {
	t.Run("writes embedded assets and calls build", func(t *testing.T) {
		projectDir := t.TempDir()

		mock := &mockDocker{buildBody: `{"stream":"Successfully built abc123"}`}
		c := newClientWithAPI("test-project", mock)

		if err := c.BuildImage(projectDir); err != nil {
			t.Fatalf("BuildImage: %v", err)
		}

		// Verify embedded files were written to .metamorph/docker/.
		for _, name := range []string{"Dockerfile", "entrypoint.sh"} {
			path := filepath.Join(projectDir, ".metamorph", "docker", name)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Errorf("expected %s in build dir: %v", name, err)
				continue
			}
			if len(data) == 0 {
				t.Errorf("expected %s to have content", name)
			}
		}
	})

	t.Run("returns error when build fails", func(t *testing.T) {
		projectDir := t.TempDir()

		mock := &mockDocker{buildErr: fmt.Errorf("build failed")}
		c := newClientWithAPI("test-project", mock)

		err := c.BuildImage(projectDir)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "failed to build image") {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestStartAgent(t *testing.T) {
	t.Run("creates and starts container with correct config", func(t *testing.T) {
		projectDir := t.TempDir()
		upstreamDir := filepath.Join(projectDir, ".metamorph", "upstream.git")
		_ = os.MkdirAll(upstreamDir, 0755)

		mock := &mockDocker{
			createResp: container.CreateResponse{ID: "container-abc123"},
		}
		c := newClientWithAPI("myproj", mock)

		opts := AgentOpts{
			ProjectDir: projectDir,
			AgentID:    1,
			Role:       "developer",
			Model:      "claude-sonnet",
			APIKey:     "sk-test-key",
		}

		id, err := c.StartAgent(context.Background(), opts)
		if err != nil {
			t.Fatalf("StartAgent: %v", err)
		}
		if id != "container-abc123" {
			t.Errorf("container ID = %q, want %q", id, "container-abc123")
		}

		// Verify container was created with expected name.
		if len(mock.created) != 1 {
			t.Fatalf("expected 1 create call, got %d", len(mock.created))
		}
		call := mock.created[0]
		if call.Name != "metamorph-myproj-agent-1" {
			t.Errorf("container name = %q, want %q", call.Name, "metamorph-myproj-agent-1")
		}

		// Verify labels.
		if call.Config.Labels[labelProject] != "myproj" {
			t.Errorf("project label = %q", call.Config.Labels[labelProject])
		}
		if call.Config.Labels[labelAgentID] != "1" {
			t.Errorf("agent-id label = %q", call.Config.Labels[labelAgentID])
		}

		// Verify environment.
		envMap := map[string]string{}
		for _, e := range call.Config.Env {
			parts := strings.SplitN(e, "=", 2)
			envMap[parts[0]] = parts[1]
		}
		if envMap["AGENT_ID"] != "1" {
			t.Errorf("AGENT_ID = %q", envMap["AGENT_ID"])
		}
		if envMap["AGENT_ROLE"] != "developer" {
			t.Errorf("AGENT_ROLE = %q", envMap["AGENT_ROLE"])
		}
		if envMap["AGENT_MODEL"] != "claude-sonnet" {
			t.Errorf("AGENT_MODEL = %q", envMap["AGENT_MODEL"])
		}
		if envMap["ANTHROPIC_API_KEY"] != "sk-test-key" {
			t.Errorf("ANTHROPIC_API_KEY = %q", envMap["ANTHROPIC_API_KEY"])
		}

		// Verify mounts.
		if len(call.Host.Mounts) != 2 {
			t.Fatalf("expected 2 mounts, got %d", len(call.Host.Mounts))
		}
		upstreamMount := call.Host.Mounts[0]
		if upstreamMount.Target != "/upstream" || upstreamMount.ReadOnly {
			t.Errorf("upstream mount: target=%q, ro=%v (expected ro=false)", upstreamMount.Target, upstreamMount.ReadOnly)
		}
		logsMount := call.Host.Mounts[1]
		if logsMount.Target != "/workspace/logs" {
			t.Errorf("logs mount target = %q", logsMount.Target)
		}

		// Verify restart policy.
		if call.Host.RestartPolicy.Name != container.RestartPolicyUnlessStopped {
			t.Errorf("restart policy = %q", call.Host.RestartPolicy.Name)
		}

		// Verify container was started.
		if len(mock.started) != 1 || mock.started[0] != "container-abc123" {
			t.Errorf("started = %v", mock.started)
		}
	})

	t.Run("returns error on create failure", func(t *testing.T) {
		projectDir := t.TempDir()
		_ = os.MkdirAll(filepath.Join(projectDir, ".metamorph", "upstream.git"), 0755)

		mock := &mockDocker{createErr: fmt.Errorf("no space")}
		c := newClientWithAPI("proj", mock)

		_, err := c.StartAgent(context.Background(), AgentOpts{ProjectDir: projectDir, AgentID: 1})
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "failed to create container") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("returns error on start failure", func(t *testing.T) {
		projectDir := t.TempDir()
		_ = os.MkdirAll(filepath.Join(projectDir, ".metamorph", "upstream.git"), 0755)

		mock := &mockDocker{
			createResp: container.CreateResponse{ID: "cid"},
			startErr:   fmt.Errorf("port conflict"),
		}
		c := newClientWithAPI("proj", mock)

		_, err := c.StartAgent(context.Background(), AgentOpts{ProjectDir: projectDir, AgentID: 2})
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "failed to start container") {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestStopAgent(t *testing.T) {
	t.Run("stops and removes container", func(t *testing.T) {
		mock := &mockDocker{
			listResult: []types.Container{
				{ID: "cid-123", Labels: map[string]string{labelProject: "proj", labelAgentID: "1"}},
			},
		}
		c := newClientWithAPI("proj", mock)

		if err := c.StopAgent(context.Background(), 1); err != nil {
			t.Fatalf("StopAgent: %v", err)
		}
		if len(mock.stopped) != 1 || mock.stopped[0] != "cid-123" {
			t.Errorf("stopped = %v", mock.stopped)
		}
		if len(mock.removed) != 1 || mock.removed[0] != "cid-123" {
			t.Errorf("removed = %v", mock.removed)
		}
	})

	t.Run("returns error when not found", func(t *testing.T) {
		mock := &mockDocker{listResult: []types.Container{}}
		c := newClientWithAPI("proj", mock)

		err := c.StopAgent(context.Background(), 99)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "no container found") {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestStopAllAgents(t *testing.T) {
	t.Run("stops all project containers", func(t *testing.T) {
		mock := &mockDocker{
			listResult: []types.Container{
				{ID: "cid-aaa0000000000", Labels: map[string]string{labelProject: "proj", labelAgentID: "1"}},
				{ID: "cid-bbb0000000000", Labels: map[string]string{labelProject: "proj", labelAgentID: "2"}},
			},
		}
		c := newClientWithAPI("proj", mock)

		if err := c.StopAllAgents(context.Background()); err != nil {
			t.Fatalf("StopAllAgents: %v", err)
		}
		if len(mock.stopped) != 2 {
			t.Errorf("stopped %d containers, want 2", len(mock.stopped))
		}
		if len(mock.removed) != 2 {
			t.Errorf("removed %d containers, want 2", len(mock.removed))
		}
	})

	t.Run("no containers is not an error", func(t *testing.T) {
		mock := &mockDocker{listResult: []types.Container{}}
		c := newClientWithAPI("proj", mock)

		if err := c.StopAllAgents(context.Background()); err != nil {
			t.Fatalf("StopAllAgents: %v", err)
		}
	})
}

func TestListAgents(t *testing.T) {
	t.Run("returns agent info from containers", func(t *testing.T) {
		startTime := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)

		mock := &mockDocker{
			listResult: []types.Container{
				{ID: "cid-111", Status: "Up 2 hours", Labels: map[string]string{labelProject: "proj", labelAgentID: "1"}},
			},
			inspectResp: types.ContainerJSON{
				Config: &container.Config{
					Env: []string{"AGENT_ID=1", "AGENT_ROLE=developer", "AGENT_MODEL=claude-sonnet"},
				},
				ContainerJSONBase: &types.ContainerJSONBase{
					State: &types.ContainerState{
						StartedAt: startTime.Format(time.RFC3339Nano),
					},
				},
			},
		}
		c := newClientWithAPI("proj", mock)

		agents, err := c.ListAgents(context.Background())
		if err != nil {
			t.Fatalf("ListAgents: %v", err)
		}
		if len(agents) != 1 {
			t.Fatalf("expected 1 agent, got %d", len(agents))
		}

		a := agents[0]
		if a.ID != 1 {
			t.Errorf("ID = %d", a.ID)
		}
		if a.ContainerID != "cid-111" {
			t.Errorf("ContainerID = %q", a.ContainerID)
		}
		if a.Role != "developer" {
			t.Errorf("Role = %q", a.Role)
		}
		if a.Status != "Up 2 hours" {
			t.Errorf("Status = %q", a.Status)
		}
		if !a.StartedAt.Equal(startTime) {
			t.Errorf("StartedAt = %v, want %v", a.StartedAt, startTime)
		}
	})

	t.Run("empty list", func(t *testing.T) {
		mock := &mockDocker{listResult: []types.Container{}}
		c := newClientWithAPI("proj", mock)

		agents, err := c.ListAgents(context.Background())
		if err != nil {
			t.Fatalf("ListAgents: %v", err)
		}
		if len(agents) != 0 {
			t.Errorf("expected 0 agents, got %d", len(agents))
		}
	})
}

func TestGetLogs(t *testing.T) {
	t.Run("returns log stream", func(t *testing.T) {
		// Build properly framed Docker log output.
		framed := dockerFrame("line1\nline2\n")

		mock := &mockDocker{
			listResult: []types.Container{
				{ID: "cid-111", Labels: map[string]string{labelProject: "proj", labelAgentID: "1"}},
			},
			logsBody: string(framed),
		}
		c := newClientWithAPI("proj", mock)

		reader, err := c.GetLogs(context.Background(), 1, 100, false)
		if err != nil {
			t.Fatalf("GetLogs: %v", err)
		}
		defer func() { _ = reader.Close() }()

		var buf bytes.Buffer
		_, _ = io.Copy(&buf, reader)
		if !strings.Contains(buf.String(), "line1") {
			t.Errorf("expected log output containing 'line1', got %q", buf.String())
		}
	})

	t.Run("returns error when container not found", func(t *testing.T) {
		mock := &mockDocker{listResult: []types.Container{}}
		c := newClientWithAPI("proj", mock)

		_, err := c.GetLogs(context.Background(), 99, 50, false)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "no container found") {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestContainerNamingConvention(t *testing.T) {
	tests := []struct {
		project string
		agentID int
		want    string
	}{
		{"myproj", 1, "metamorph-myproj-agent-1"},
		{"myproj", 2, "metamorph-myproj-agent-2"},
		{"web-app", 10, "metamorph-web-app-agent-10"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			projectDir := t.TempDir()
			_ = os.MkdirAll(filepath.Join(projectDir, ".metamorph", "upstream.git"), 0755)

			mock := &mockDocker{
				createResp: container.CreateResponse{ID: "test-id"},
			}
			c := newClientWithAPI(tt.project, mock)

			_, _ = c.StartAgent(context.Background(), AgentOpts{
				ProjectDir: projectDir,
				AgentID:    tt.agentID,
			})

			if len(mock.created) != 1 {
				t.Fatalf("expected 1 create call, got %d", len(mock.created))
			}
			if mock.created[0].Name != tt.want {
				t.Errorf("container name = %q, want %q", mock.created[0].Name, tt.want)
			}
		})
	}
}

func TestStopAllAgents_CallsStopForEach(t *testing.T) {
	mock := &mockDocker{
		listResult: []types.Container{
			{ID: "aaa0000000000000", Labels: map[string]string{labelProject: "proj", labelAgentID: "1"}},
			{ID: "bbb0000000000000", Labels: map[string]string{labelProject: "proj", labelAgentID: "2"}},
			{ID: "ccc0000000000000", Labels: map[string]string{labelProject: "proj", labelAgentID: "3"}},
		},
	}
	c := newClientWithAPI("proj", mock)

	if err := c.StopAllAgents(context.Background()); err != nil {
		t.Fatalf("StopAllAgents: %v", err)
	}

	if len(mock.stopped) != 3 {
		t.Errorf("expected 3 stops, got %d", len(mock.stopped))
	}
	if len(mock.removed) != 3 {
		t.Errorf("expected 3 removes, got %d", len(mock.removed))
	}
}

func TestStartAgent_PassesCorrectConfig(t *testing.T) {
	projectDir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(projectDir, ".metamorph", "upstream.git"), 0755)

	mock := &mockDocker{
		createResp: container.CreateResponse{ID: "cid-test"},
	}
	c := newClientWithAPI("test-proj", mock)

	opts := AgentOpts{
		ProjectDir: projectDir,
		AgentID:    3,
		Role:       "tester",
		Model:      "claude-opus",
		APIKey:     "sk-test-123",
	}

	_, err := c.StartAgent(context.Background(), opts)
	if err != nil {
		t.Fatalf("StartAgent: %v", err)
	}

	call := mock.created[0]
	if call.Config.Image != defaultImageTag {
		t.Errorf("image = %q, want %q", call.Config.Image, defaultImageTag)
	}

	envMap := map[string]string{}
	for _, e := range call.Config.Env {
		parts := strings.SplitN(e, "=", 2)
		envMap[parts[0]] = parts[1]
	}
	if envMap["AGENT_ID"] != "3" {
		t.Errorf("AGENT_ID = %q", envMap["AGENT_ID"])
	}
	if envMap["AGENT_ROLE"] != "tester" {
		t.Errorf("AGENT_ROLE = %q", envMap["AGENT_ROLE"])
	}
	if envMap["AGENT_MODEL"] != "claude-opus" {
		t.Errorf("AGENT_MODEL = %q", envMap["AGENT_MODEL"])
	}
	if envMap["ANTHROPIC_API_KEY"] != "sk-test-123" {
		t.Errorf("ANTHROPIC_API_KEY = %q", envMap["ANTHROPIC_API_KEY"])
	}
}

func TestStartAgent_PassesOAuthToken(t *testing.T) {
	projectDir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(projectDir, ".metamorph", "upstream.git"), 0755)

	mock := &mockDocker{
		createResp: container.CreateResponse{ID: "cid-oauth"},
	}
	c := newClientWithAPI("test-proj", mock)

	opts := AgentOpts{
		ProjectDir: projectDir,
		AgentID:    1,
		Role:       "developer",
		Model:      "claude-opus",
		OAuthToken: "oauth-token-123",
	}

	_, err := c.StartAgent(context.Background(), opts)
	if err != nil {
		t.Fatalf("StartAgent: %v", err)
	}

	envMap := map[string]string{}
	for _, e := range mock.created[0].Config.Env {
		parts := strings.SplitN(e, "=", 2)
		envMap[parts[0]] = parts[1]
	}
	if envMap["CLAUDE_CODE_OAUTH_TOKEN"] != "oauth-token-123" {
		t.Errorf("CLAUDE_CODE_OAUTH_TOKEN = %q, want %q", envMap["CLAUDE_CODE_OAUTH_TOKEN"], "oauth-token-123")
	}
	if _, ok := envMap["ANTHROPIC_API_KEY"]; ok {
		t.Errorf("ANTHROPIC_API_KEY should not be set when only OAuthToken is provided")
	}
}

func TestStartAgent_OAuthTakesPrecedence(t *testing.T) {
	projectDir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(projectDir, ".metamorph", "upstream.git"), 0755)

	mock := &mockDocker{
		createResp: container.CreateResponse{ID: "cid-both"},
	}
	c := newClientWithAPI("test-proj", mock)

	opts := AgentOpts{
		ProjectDir: projectDir,
		AgentID:    1,
		Role:       "developer",
		Model:      "claude-opus",
		APIKey:     "sk-test-123",
		OAuthToken: "oauth-token-123",
	}

	_, err := c.StartAgent(context.Background(), opts)
	if err != nil {
		t.Fatalf("StartAgent: %v", err)
	}

	envMap := map[string]string{}
	for _, e := range mock.created[0].Config.Env {
		parts := strings.SplitN(e, "=", 2)
		envMap[parts[0]] = parts[1]
	}
	if envMap["CLAUDE_CODE_OAUTH_TOKEN"] != "oauth-token-123" {
		t.Errorf("CLAUDE_CODE_OAUTH_TOKEN = %q", envMap["CLAUDE_CODE_OAUTH_TOKEN"])
	}
	if _, ok := envMap["ANTHROPIC_API_KEY"]; ok {
		t.Errorf("ANTHROPIC_API_KEY should not be set when OAuthToken is provided")
	}
}

func TestDockerClientInterface(t *testing.T) {
	// Verify the interface is usable with a mock.
	var _ DockerClient = (*Client)(nil)
	var _ DockerClient = (*mockDockerClient)(nil)
}

// mockDockerClient is a full mock of the DockerClient interface for consumers.
type mockDockerClient struct{}

func (m *mockDockerClient) BuildImage(projectDir string) error { return nil }
func (m *mockDockerClient) StartAgent(ctx context.Context, opts AgentOpts) (string, error) {
	return "mock-id", nil
}
func (m *mockDockerClient) StopAgent(ctx context.Context, agentID int) error { return nil }
func (m *mockDockerClient) StopAllAgents(ctx context.Context) error          { return nil }
func (m *mockDockerClient) ListAgents(ctx context.Context) ([]AgentInfo, error) {
	return nil, nil
}
func (m *mockDockerClient) GetLogs(ctx context.Context, agentID int, tail int, follow bool) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func TestEnvValue(t *testing.T) {
	tests := []struct {
		env  []string
		key  string
		want string
	}{
		{[]string{"FOO=bar", "BAZ=qux"}, "FOO", "bar"},
		{[]string{"FOO=bar", "BAZ=qux"}, "BAZ", "qux"},
		{[]string{"FOO=bar"}, "MISSING", ""},
		{[]string{"KEY=val=ue"}, "KEY", "val=ue"},
		{nil, "FOO", ""},
	}

	for _, tt := range tests {
		got := envValue(tt.env, tt.key)
		if got != tt.want {
			t.Errorf("envValue(%v, %q) = %q, want %q", tt.env, tt.key, got, tt.want)
		}
	}
}

func TestCreateTarContext(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM ubuntu\n"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "entrypoint.sh"), []byte("#!/bin/bash\n"), 0644)

	reader, err := createTarContext(dir)
	if err != nil {
		t.Fatalf("createTarContext: %v", err)
	}

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, reader); err != nil {
		t.Fatalf("reading tar: %v", err)
	}

	if buf.Len() == 0 {
		t.Error("expected non-empty tar archive")
	}
}

func TestStopAllAgents_StopError(t *testing.T) {
	mock := &mockDocker{
		listResult: []types.Container{
			{ID: "aaa0000000000000", Labels: map[string]string{labelProject: "proj", labelAgentID: "1"}},
		},
		stopErr: fmt.Errorf("permission denied"),
	}
	c := newClientWithAPI("proj", mock)

	err := c.StopAllAgents(context.Background())
	if err == nil {
		t.Fatal("expected error when stop fails")
	}
	if !strings.Contains(err.Error(), "errors stopping agents") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestStopAllAgents_RemoveError(t *testing.T) {
	mock := &mockDocker{
		listResult: []types.Container{
			{ID: "aaa0000000000000", Labels: map[string]string{labelProject: "proj", labelAgentID: "1"}},
		},
		removeErr: fmt.Errorf("still running"),
	}
	c := newClientWithAPI("proj", mock)

	err := c.StopAllAgents(context.Background())
	if err == nil {
		t.Fatal("expected error when remove fails")
	}
	if !strings.Contains(err.Error(), "errors stopping agents") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestListAgents_Error(t *testing.T) {
	mock := &mockDocker{
		listErr: fmt.Errorf("docker daemon not running"),
	}
	c := newClientWithAPI("proj", mock)

	_, err := c.ListAgents(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "failed to list containers") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestStopAgent_StopError(t *testing.T) {
	mock := &mockDocker{
		listResult: []types.Container{
			{ID: "cid-123", Labels: map[string]string{labelProject: "proj", labelAgentID: "1"}},
		},
		stopErr: fmt.Errorf("timeout"),
	}
	c := newClientWithAPI("proj", mock)

	err := c.StopAgent(context.Background(), 1)
	if err == nil {
		t.Fatal("expected error when stop fails")
	}
}
