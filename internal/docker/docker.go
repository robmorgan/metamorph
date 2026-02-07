package docker

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	dockerclient "github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/robmorgan/metamorph/assets"
	"github.com/robmorgan/metamorph/internal/constants"
)

const (
	defaultImageTag  = "metamorph-agent:latest"
	labelProject     = "metamorph.project"
	labelAgentID     = "metamorph.agent-id"
	stopTimeout      = 30 // seconds
	buildTimeout     = 5 * time.Minute
	startStopTimeout = 30 * time.Second
	listTimeout      = 10 * time.Second
)

// AgentOpts configures a new agent container.
type AgentOpts struct {
	ProjectDir     string
	AgentID        int
	Role           string
	Model          string
	APIKey         string // Anthropic API key (if set)
	OAuthToken     string // Claude Code OAuth token (if set, preferred over APIKey)
	GitAuthorName  string // Git author name for commits (optional)
	GitAuthorEmail string // Git author email for commits (optional)
}

// AgentInfo describes a running agent container.
type AgentInfo struct {
	ID          int
	ContainerID string
	Role        string
	Status      string
	StartedAt   time.Time
}

// DockerClient is the interface for Docker operations so the daemon and CLI
// can be tested without a real Docker daemon.
type DockerClient interface {
	BuildImage(projectDir string) error
	StartAgent(ctx context.Context, opts AgentOpts) (string, error)
	StopAgent(ctx context.Context, agentID int) error
	StopAllAgents(ctx context.Context) error
	ListAgents(ctx context.Context) ([]AgentInfo, error)
	GetLogs(ctx context.Context, agentID int, tail int, follow bool) (io.ReadCloser, error)
}

// dockerAPI is the subset of the Docker SDK client we use, enabling test mocks.
type dockerAPI interface {
	Ping(ctx context.Context) (types.Ping, error)
	ImageBuild(ctx context.Context, buildContext io.Reader, options types.ImageBuildOptions) (types.ImageBuildResponse, error)
	ContainerCreate(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error)
	ContainerStart(ctx context.Context, containerID string, options container.StartOptions) error
	ContainerStop(ctx context.Context, containerID string, options container.StopOptions) error
	ContainerRemove(ctx context.Context, containerID string, options container.RemoveOptions) error
	ContainerList(ctx context.Context, options container.ListOptions) ([]types.Container, error)
	ContainerInspect(ctx context.Context, containerID string) (types.ContainerJSON, error)
	ContainerLogs(ctx context.Context, container string, options container.LogsOptions) (io.ReadCloser, error)
}

// Client manages Docker containers for metamorph agents.
type Client struct {
	cli         dockerAPI
	projectName string
}

// Verify Client implements DockerClient at compile time.
var _ DockerClient = (*Client)(nil)

// NewClient creates a Docker API client and verifies connectivity.
func NewClient(projectName string) (*Client, error) {
	cli, err := dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker: failed to create client: %w", err)
	}

	if _, err := cli.Ping(context.Background()); err != nil {
		return nil, fmt.Errorf("docker: daemon is not running (is Docker started?): %w", err)
	}

	return &Client{cli: cli, projectName: projectName}, nil
}

// newClientWithAPI creates a Client with a provided dockerAPI (for testing).
func newClientWithAPI(projectName string, api dockerAPI) *Client {
	return &Client{cli: api, projectName: projectName}
}

// BuildImage writes the embedded Dockerfile and entrypoint into .metamorph/docker/,
// creates a tar build context, and builds the image.
func (c *Client) BuildImage(projectDir string) error {
	buildDir := filepath.Join(projectDir, constants.DockerDir)
	if err := os.MkdirAll(buildDir, 0755); err != nil {
		return fmt.Errorf("docker: failed to create build dir: %w", err)
	}

	// Write embedded assets to the build directory.
	embeddedFiles := map[string]string{
		"Dockerfile":       assets.DefaultDockerfile,
		"entrypoint.sh":    assets.DefaultEntrypoint,
		"system_prompt.md": assets.SystemPrompt,
	}
	for name, content := range embeddedFiles {
		dst := filepath.Join(buildDir, name)
		if err := os.WriteFile(dst, []byte(content), 0755); err != nil {
			return fmt.Errorf("docker: failed to write %s: %w", name, err)
		}
	}

	buildCtx, err := createTarContext(buildDir)
	if err != nil {
		return fmt.Errorf("docker: failed to create build context: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), buildTimeout)
	defer cancel()

	resp, err := c.cli.ImageBuild(ctx, buildCtx, types.ImageBuildOptions{
		Tags:       []string{defaultImageTag},
		Dockerfile: "Dockerfile",
		Remove:     true,
	})
	if err != nil {
		return fmt.Errorf("docker: failed to build image: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Read the build output stream and check for errors.
	scanner := bufio.NewScanner(resp.Body)
	var buildErr string
	for scanner.Scan() {
		var msg struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &msg); err == nil && msg.Error != "" {
			buildErr = msg.Error
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("docker: failed to read build output: %w", err)
	}
	if buildErr != "" {
		return fmt.Errorf("docker: image build failed: %s", buildErr)
	}

	return nil
}

// StartAgent creates and starts a container for the given agent.
func (c *Client) StartAgent(ctx context.Context, opts AgentOpts) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, startStopTimeout)
	defer cancel()

	containerName := fmt.Sprintf("metamorph-%s-agent-%d", c.projectName, opts.AgentID)
	agentIDStr := strconv.Itoa(opts.AgentID)

	// Remove any stale container with the same name so we can recreate it.
	_ = c.cli.ContainerStop(ctx, containerName, container.StopOptions{})
	_ = c.cli.ContainerRemove(ctx, containerName, container.RemoveOptions{})

	upstreamAbs, err := filepath.Abs(filepath.Join(opts.ProjectDir, constants.UpstreamDir))
	if err != nil {
		return "", fmt.Errorf("docker: failed to resolve upstream path: %w", err)
	}

	agentPromptAbs, err := filepath.Abs(filepath.Join(opts.ProjectDir, constants.AgentPromptFile))
	if err != nil {
		return "", fmt.Errorf("docker: failed to resolve agent prompt path: %w", err)
	}
	if _, err := os.Stat(agentPromptAbs); os.IsNotExist(err) {
		return "", fmt.Errorf("docker: %s not found in project directory (run 'metamorph init' first)", constants.AgentPromptFile)
	}

	logDir := filepath.Join(opts.ProjectDir, constants.AgentLogDir, fmt.Sprintf("agent-%d", opts.AgentID))
	logDirAbs, err := filepath.Abs(logDir)
	if err != nil {
		return "", fmt.Errorf("docker: failed to resolve log dir: %w", err)
	}
	if err := os.MkdirAll(logDirAbs, 0755); err != nil {
		return "", fmt.Errorf("docker: failed to create log dir: %w", err)
	}

	env := []string{
		"AGENT_ID=" + agentIDStr,
		"AGENT_ROLE=" + opts.Role,
		"AGENT_MODEL=" + opts.Model,
	}
	if opts.OAuthToken != "" {
		env = append(env, "CLAUDE_CODE_OAUTH_TOKEN="+opts.OAuthToken)
	} else if opts.APIKey != "" {
		env = append(env, "ANTHROPIC_API_KEY="+opts.APIKey)
	}
	if opts.GitAuthorName != "" {
		env = append(env, "GIT_AUTHOR_NAME="+opts.GitAuthorName)
	}
	if opts.GitAuthorEmail != "" {
		env = append(env, "GIT_AUTHOR_EMAIL="+opts.GitAuthorEmail)
	}

	config := &container.Config{
		Image: defaultImageTag,
		Env:   env,
		Labels: map[string]string{
			labelProject: c.projectName,
			labelAgentID: agentIDStr,
		},
	}

	hostConfig := &container.HostConfig{
		Mounts: []mount.Mount{
			{
				Type:   mount.TypeBind,
				Source: upstreamAbs,
				Target: "/upstream",
			},
			{
				Type:   mount.TypeBind,
				Source: logDirAbs,
				Target: "/workspace/logs",
			},
			{
				Type:     mount.TypeBind,
				Source:   agentPromptAbs,
				Target:   "/workspace/agent_prompt.md",
				ReadOnly: true,
			},
		},
		RestartPolicy: container.RestartPolicy{
			Name: container.RestartPolicyUnlessStopped,
		},
	}

	resp, err := c.cli.ContainerCreate(ctx, config, hostConfig, nil, nil, containerName)
	if err != nil {
		return "", fmt.Errorf("docker: failed to create container for agent-%d: %w", opts.AgentID, err)
	}

	if err := c.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("docker: failed to start container for agent-%d: %w", opts.AgentID, err)
	}

	return resp.ID, nil
}

// StopAgent stops and removes the container for the given agent.
func (c *Client) StopAgent(ctx context.Context, agentID int) error {
	ctx, cancel := context.WithTimeout(ctx, startStopTimeout)
	defer cancel()

	containerID, err := c.findContainer(ctx, agentID)
	if err != nil {
		return err
	}

	timeout := stopTimeout
	if err := c.cli.ContainerStop(ctx, containerID, container.StopOptions{Timeout: &timeout}); err != nil {
		return fmt.Errorf("docker: failed to stop agent-%d: %w", agentID, err)
	}

	if err := c.cli.ContainerRemove(ctx, containerID, container.RemoveOptions{}); err != nil {
		return fmt.Errorf("docker: failed to remove agent-%d: %w", agentID, err)
	}

	return nil
}

// StopAllAgents stops and removes all containers for this project.
func (c *Client) StopAllAgents(ctx context.Context) error {
	// Use a generous listing timeout first.
	listCtx, listCancel := context.WithTimeout(ctx, listTimeout)
	defer listCancel()

	containers, err := c.listProjectContainers(listCtx)
	if err != nil {
		return err
	}

	// Stop all containers in parallel so we fit within a single stop timeout.
	ctx, cancel := context.WithTimeout(ctx, startStopTimeout)
	defer cancel()

	var (
		mu   sync.Mutex
		errs []string
		wg   sync.WaitGroup
	)
	timeout := stopTimeout
	for _, ctr := range containers {
		wg.Add(1)
		go func(ctr types.Container) {
			defer wg.Done()
			if err := c.cli.ContainerStop(ctx, ctr.ID, container.StopOptions{Timeout: &timeout}); err != nil {
				mu.Lock()
				errs = append(errs, fmt.Sprintf("stop %s: %v", ctr.ID[:12], err))
				mu.Unlock()
				return
			}
			if err := c.cli.ContainerRemove(ctx, ctr.ID, container.RemoveOptions{}); err != nil {
				mu.Lock()
				errs = append(errs, fmt.Sprintf("remove %s: %v", ctr.ID[:12], err))
				mu.Unlock()
			}
		}(ctr)
	}
	wg.Wait()

	if len(errs) > 0 {
		return fmt.Errorf("docker: errors stopping agents: %s", strings.Join(errs, "; "))
	}
	return nil
}

// ListAgents returns info about all running agent containers for this project.
func (c *Client) ListAgents(ctx context.Context) ([]AgentInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()

	containers, err := c.listProjectContainers(ctx)
	if err != nil {
		return nil, err
	}

	var agents []AgentInfo
	for _, ctr := range containers {
		agentID, _ := strconv.Atoi(ctr.Labels[labelAgentID])

		info, err := c.cli.ContainerInspect(ctx, ctr.ID)
		if err != nil {
			return nil, fmt.Errorf("docker: failed to inspect container %s: %w", ctr.ID[:12], err)
		}

		role := envValue(info.Config.Env, "AGENT_ROLE")
		startedAt, _ := time.Parse(time.RFC3339Nano, info.State.StartedAt)

		agents = append(agents, AgentInfo{
			ID:          agentID,
			ContainerID: ctr.ID,
			Role:        role,
			Status:      ctr.Status,
			StartedAt:   startedAt,
		})
	}

	return agents, nil
}

// GetLogs returns a log stream from the agent's container.
func (c *Client) GetLogs(ctx context.Context, agentID int, tail int, follow bool) (io.ReadCloser, error) {
	containerID, err := c.findContainer(ctx, agentID)
	if err != nil {
		return nil, err
	}

	tailStr := "all"
	if tail > 0 {
		tailStr = strconv.Itoa(tail)
	}

	logReader, err := c.cli.ContainerLogs(ctx, containerID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     follow,
		Tail:       tailStr,
	})
	if err != nil {
		return nil, fmt.Errorf("docker: failed to get logs for agent-%d: %w", agentID, err)
	}

	// Docker multiplexes stdout/stderr with an 8-byte header per frame.
	// Wrap with stdcopy to demux into a clean stream.
	pr, pw := io.Pipe()
	go func() {
		_, err := stdcopy.StdCopy(pw, pw, logReader)
		_ = logReader.Close()
		pw.CloseWithError(err)
	}()

	return pr, nil
}

// findContainer locates a single container by agent ID within this project.
func (c *Client) findContainer(ctx context.Context, agentID int) (string, error) {
	f := filters.NewArgs()
	f.Add("label", fmt.Sprintf("%s=%s", labelProject, c.projectName))
	f.Add("label", fmt.Sprintf("%s=%d", labelAgentID, agentID))

	containers, err := c.cli.ContainerList(ctx, container.ListOptions{All: true, Filters: f})
	if err != nil {
		return "", fmt.Errorf("docker: failed to list containers: %w", err)
	}
	if len(containers) == 0 {
		return "", fmt.Errorf("docker: no container found for agent-%d", agentID)
	}

	return containers[0].ID, nil
}

// listProjectContainers returns all containers labelled for this project.
func (c *Client) listProjectContainers(ctx context.Context) ([]types.Container, error) {
	f := filters.NewArgs()
	f.Add("label", fmt.Sprintf("%s=%s", labelProject, c.projectName))

	containers, err := c.cli.ContainerList(ctx, container.ListOptions{All: true, Filters: f})
	if err != nil {
		return nil, fmt.Errorf("docker: failed to list containers: %w", err)
	}
	return containers, nil
}

// createTarContext creates an in-memory tar archive of the given directory.
func createTarContext(dir string) (io.Reader, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = rel

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()

		_, err = io.Copy(tw, f)
		return err
	})
	if err != nil {
		return nil, err
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}

	return &buf, nil
}

// envValue extracts a value from a slice of "KEY=VALUE" strings.
func envValue(env []string, key string) string {
	prefix := key + "="
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return strings.TrimPrefix(e, prefix)
		}
	}
	return ""
}
