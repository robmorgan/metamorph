package cmd

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/robmorgan/metamorph/internal/controlplane"
	"github.com/robmorgan/metamorph/internal/config"
	"github.com/robmorgan/metamorph/internal/constants"
)

// setupTestHome overrides HOME so all ~/.metamorph/ paths resolve to a temp dir.
func setupTestHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	return dir
}

// testProject creates a temp dir with a valid project config registered in the
// global ~/.metamorph/ directory. Returns the repo dir path.
func testProject(t *testing.T) string {
	t.Helper()
	home := setupTestHome(t)
	repoDir := filepath.Join(t.TempDir(), "test-proj")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatal(err)
	}

	projectName := "test-proj"

	// Write project config under ~/.metamorph/projects/test-proj/.
	projectDir := filepath.Join(home, constants.GlobalDirName, constants.ProjectsDir, projectName)
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Project: config.ProjectConfig{
			Name:        projectName,
			Description: "Test project",
		},
		Repos: []config.RepoConfig{
			{Path: repoDir},
		},
		Agents: config.AgentsConfig{
			Count: 2,
			Model: "claude-sonnet",
			Roles: []string{"developer", "tester"},
		},
		Docker: config.DockerConfig{
			Image: "metamorph-agent:latest",
		},
		Testing: config.TestingConfig{
			Command: "go test ./...",
		},
	}
	configPath := filepath.Join(projectDir, constants.ProjectConfigFile)
	if err := config.Save(cfg, configPath); err != nil {
		t.Fatal(err)
	}

	// Write files in the repo dir.
	if err := os.WriteFile(filepath.Join(repoDir, constants.AgentPromptFile), []byte("# Test Agent Prompt\nHello ${AGENT_ID}"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, constants.ProgressFile), []byte("# Progress\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repoDir, constants.TaskLockDir), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".gitignore"), []byte("current_tasks/\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Register project in broker DB.
	dbPath := filepath.Join(home, constants.GlobalDirName, constants.ControlPlaneDBFile)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		t.Fatal(err)
	}
	store, err := controlplane.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.RegisterProject(ctx, projectName); err != nil {
		t.Fatal(err)
	}
	if err := store.AddProjectRepo(ctx, projectName, repoDir, ""); err != nil {
		t.Fatal(err)
	}
	store.Close()

	return repoDir
}

// testProjectWithUpstream creates a testProject, initializes it as a git repo
// with an initial commit, then clones it as a bare upstream repo.
func testProjectWithUpstream(t *testing.T) string {
	t.Helper()
	dir := testProject(t)
	projectName := "test-proj"

	// Initialize the project dir as a git repo with an initial commit.
	gitExec(t, dir, "init")
	gitExec(t, dir, "config", "user.name", "test")
	gitExec(t, dir, "config", "user.email", "test@test")
	gitExec(t, dir, "add", ".")
	gitExec(t, dir, "commit", "-m", "initial commit")

	// Clone the project as a bare upstream repo for each configured repo.
	// Load config to get repo labels.
	cfg, err := config.LoadProject(projectName)
	if err != nil {
		t.Fatal(err)
	}
	for _, repo := range cfg.Repos {
		label := config.RepoLabel(repo)
		upstreamPath := constants.UpstreamRepoPath(projectName, label)
		if err := os.MkdirAll(filepath.Dir(upstreamPath), 0755); err != nil {
			t.Fatal(err)
		}
		gitExec(t, repo.Path, "clone", "--bare", ".", upstreamPath)
	}

	// Use primary repo for scaffold seeding.
	primaryLabel := config.RepoLabel(cfg.Repos[0])
	upstreamPath := constants.UpstreamRepoPath(projectName, primaryLabel)

	// Add scaffold files if needed (current_tasks/.gitkeep).
	tmpDir := t.TempDir()
	cloneDir := filepath.Join(tmpDir, "seed")
	gitExec(t, tmpDir, "clone", upstreamPath, cloneDir)
	gitExec(t, cloneDir, "config", "user.name", "test")
	gitExec(t, cloneDir, "config", "user.email", "test@test")

	needsCommit := false
	taskLockDir := filepath.Join(cloneDir, constants.TaskLockDir)
	gitkeep := filepath.Join(taskLockDir, ".gitkeep")
	if _, err := os.Stat(gitkeep); os.IsNotExist(err) {
		if err := os.MkdirAll(taskLockDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(gitkeep, []byte(""), 0644); err != nil {
			t.Fatal(err)
		}
		needsCommit = true
	}

	if needsCommit {
		gitExec(t, cloneDir, "add", "-f", ".")
		gitExec(t, cloneDir, "commit", "-m", "seed scaffold files")
		branch := gitOutput(t, cloneDir, "rev-parse", "--abbrev-ref", "HEAD")
		gitExec(t, cloneDir, "push", "origin", branch)
	}

	return dir
}

// gitExec runs a git command in dir, failing the test on error.
func gitExec(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, stderr.String())
	}
}

// gitOutput runs a git command and returns trimmed stdout.
func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}

func TestProjectCreateCreatesAllFiles(t *testing.T) {
	home := setupTestHome(t)
	dir := filepath.Join(t.TempDir(), "new-project")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	gitExec(t, dir, "init")
	gitExec(t, dir, "config", "user.name", "test")
	gitExec(t, dir, "config", "user.email", "test@test")
	if err := os.WriteFile(filepath.Join(dir, ".gitkeep"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	gitExec(t, dir, "add", ".")
	gitExec(t, dir, "commit", "-m", "initial commit")

	rootCmd.SetArgs([]string{"project", "create", dir})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("project create: %v", err)
	}

	projectName := filepath.Base(dir)

	// Check project config.
	configPath := filepath.Join(home, constants.GlobalDirName, constants.ProjectsDir, projectName, constants.ProjectConfigFile)
	if _, err := os.Stat(configPath); err != nil {
		t.Errorf("expected config.json to exist at %s: %v", configPath, err)
	}

	// Check files in repo dir.
	for _, f := range []string{
		constants.AgentPromptFile,
		constants.ProgressFile,
		".gitignore",
	} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("expected %s to exist: %v", f, err)
		}
	}

	// Check directories.
	info, err := os.Stat(filepath.Join(dir, constants.TaskLockDir))
	if err != nil {
		t.Errorf("expected %s to exist: %v", constants.TaskLockDir, err)
	} else if !info.IsDir() {
		t.Errorf("expected %s to be a directory", constants.TaskLockDir)
	}

	// Verify config content.
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, projectName) {
		t.Errorf("config should contain project name %q", projectName)
	}
	if !strings.Contains(content, `"count": 4`) {
		t.Error("config should have default count 4")
	}

	// Verify AGENT_PROMPT.md has skeleton template content.
	promptData, err := os.ReadFile(filepath.Join(dir, constants.AgentPromptFile))
	if err != nil {
		t.Fatalf("read agent prompt: %v", err)
	}
	promptContent := string(promptData)
	if !strings.Contains(promptContent, "# Project Instructions") {
		t.Error("AGENT_PROMPT.md should contain skeleton template")
	}
	if !strings.Contains(promptContent, "## Build & Test") {
		t.Error("AGENT_PROMPT.md should contain Build & Test section")
	}

	// Verify project registered in broker DB.
	dbPath := filepath.Join(home, constants.GlobalDirName, constants.ControlPlaneDBFile)
	store, err := controlplane.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("open broker db: %v", err)
	}
	defer store.Close()
	foundName, err := store.GetProjectByRepoPath(context.Background(), dir)
	if err != nil {
		t.Fatalf("project not registered in DB: %v", err)
	}
	if foundName != projectName {
		t.Errorf("expected project name %q, got %q", projectName, foundName)
	}
}

func TestProjectCreatePreservesExistingPrompt(t *testing.T) {
	setupTestHome(t)
	dir := filepath.Join(t.TempDir(), "existing-project")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	gitExec(t, dir, "init")
	gitExec(t, dir, "config", "user.name", "test")
	gitExec(t, dir, "config", "user.email", "test@test")

	// Write a custom AGENT_PROMPT.md before project create.
	customContent := "# My Custom Prompt\nDo special things.\n"
	if err := os.WriteFile(filepath.Join(dir, constants.AgentPromptFile), []byte(customContent), 0644); err != nil {
		t.Fatal(err)
	}

	gitExec(t, dir, "add", ".")
	gitExec(t, dir, "commit", "-m", "initial commit")

	rootCmd.SetArgs([]string{"project", "create", dir})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("project create: %v", err)
	}

	// Verify custom content was preserved.
	data, err := os.ReadFile(filepath.Join(dir, constants.AgentPromptFile))
	if err != nil {
		t.Fatalf("read agent prompt: %v", err)
	}
	if string(data) != customContent {
		t.Errorf("AGENT_PROMPT.md was overwritten, got: %q", string(data))
	}
}

func TestProjectCreateRejectsExistingProject(t *testing.T) {
	home := setupTestHome(t)
	dir := filepath.Join(t.TempDir(), "dup-project")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	gitExec(t, dir, "init")
	gitExec(t, dir, "config", "user.name", "test")
	gitExec(t, dir, "config", "user.email", "test@test")
	if err := os.WriteFile(filepath.Join(dir, ".gitkeep"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	gitExec(t, dir, "add", ".")
	gitExec(t, dir, "commit", "-m", "initial commit")

	// Pre-create project config to simulate existing project.
	projectName := filepath.Base(dir)
	projectDir := filepath.Join(home, constants.GlobalDirName, constants.ProjectsDir, projectName)
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, constants.ProjectConfigFile), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	rootCmd.SetArgs([]string{"project", "create", dir})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for existing project")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestProjectListEmpty(t *testing.T) {
	home := setupTestHome(t)

	// Create an empty DB.
	dbPath := filepath.Join(home, constants.GlobalDirName, constants.ControlPlaneDBFile)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		t.Fatal(err)
	}
	store, err := controlplane.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	store.Close()

	// Capture stdout.
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	rootCmd.SetArgs([]string{"project", "list"})
	err = rootCmd.Execute()

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	output := buf.String()

	if err != nil {
		t.Fatalf("project list: %v", err)
	}
	if !strings.Contains(output, "No projects registered") {
		t.Errorf("expected 'No projects registered' message, got: %q", output)
	}
}

func TestProjectListShowsProjects(t *testing.T) {
	home := setupTestHome(t)

	// Register a project in the DB.
	dbPath := filepath.Join(home, constants.GlobalDirName, constants.ControlPlaneDBFile)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		t.Fatal(err)
	}
	store, err := controlplane.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.RegisterProject(ctx, "my-project"); err != nil {
		t.Fatal(err)
	}
	if err := store.AddProjectRepo(ctx, "my-project", "/tmp/repo", "repo"); err != nil {
		t.Fatal(err)
	}
	store.Close()

	// Capture stdout.
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	rootCmd.SetArgs([]string{"project", "list"})
	err = rootCmd.Execute()

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	output := buf.String()

	if err != nil {
		t.Fatalf("project list: %v", err)
	}
	if !strings.Contains(output, "NAME") {
		t.Errorf("expected column headers, got: %q", output)
	}
	if !strings.Contains(output, "my-project") {
		t.Errorf("expected project name in output, got: %q", output)
	}
}

func TestProjectDeleteRemovesProject(t *testing.T) {
	home := setupTestHome(t)

	// Create a project dir and register in DB.
	projectName := "del-project"
	projectDir := filepath.Join(home, constants.GlobalDirName, constants.ProjectsDir, projectName)
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, constants.ProjectConfigFile), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(home, constants.GlobalDirName, constants.ControlPlaneDBFile)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		t.Fatal(err)
	}
	store, err := controlplane.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.RegisterProject(ctx, projectName); err != nil {
		t.Fatal(err)
	}
	store.Close()

	rootCmd.SetArgs([]string{"project", "delete", projectName})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("project delete: %v", err)
	}

	// Verify project dir is removed.
	if _, err := os.Stat(projectDir); !os.IsNotExist(err) {
		t.Error("expected project directory to be removed")
	}

	// Verify project is removed from DB.
	store, err = controlplane.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, err = store.GetProject(ctx, projectName)
	if err == nil {
		t.Error("expected project to be removed from DB")
	}
}

func TestProjectDeleteRefusesRunning(t *testing.T) {
	home := setupTestHome(t)

	projectName := "running-project"
	dbPath := filepath.Join(home, constants.GlobalDirName, constants.ControlPlaneDBFile)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		t.Fatal(err)
	}
	store, err := controlplane.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.RegisterProject(ctx, projectName); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateProjectStatus(ctx, projectName, "running"); err != nil {
		t.Fatal(err)
	}
	store.Close()

	rootCmd.SetArgs([]string{"project", "delete", projectName})
	err = rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for running project")
	}
	if !strings.Contains(err.Error(), "running") {
		t.Errorf("expected 'running' in error, got: %v", err)
	}
}

func TestProjectDeleteForceRunning(t *testing.T) {
	home := setupTestHome(t)

	projectName := "force-project"
	projectDir := filepath.Join(home, constants.GlobalDirName, constants.ProjectsDir, projectName)
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(home, constants.GlobalDirName, constants.ControlPlaneDBFile)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		t.Fatal(err)
	}
	store, err := controlplane.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.RegisterProject(ctx, projectName); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateProjectStatus(ctx, projectName, "running"); err != nil {
		t.Fatal(err)
	}
	store.Close()

	rootCmd.SetArgs([]string{"project", "delete", "--force", projectName})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("project delete --force: %v", err)
	}

	// Verify project dir is removed.
	if _, err := os.Stat(projectDir); !os.IsNotExist(err) {
		t.Error("expected project directory to be removed")
	}
}

func TestProjectDeleteNotFound(t *testing.T) {
	home := setupTestHome(t)

	dbPath := filepath.Join(home, constants.GlobalDirName, constants.ControlPlaneDBFile)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		t.Fatal(err)
	}
	store, err := controlplane.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	store.Close()

	rootCmd.SetArgs([]string{"project", "delete", "nonexistent"})
	err = rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for nonexistent project")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got: %v", err)
	}
}

func TestStatusWithoutDaemon(t *testing.T) {
	dir := testProject(t)

	oldWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()

	// Need a git repo for CWD resolution.
	gitExec(t, dir, "init")
	gitExec(t, dir, "config", "user.name", "test")
	gitExec(t, dir, "config", "user.email", "test@test")
	gitExec(t, dir, "add", ".")
	gitExec(t, dir, "commit", "-m", "initial commit")

	// Capture stdout.
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	rootCmd.SetArgs([]string{"status"})
	err := rootCmd.Execute()

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	output := buf.String()

	if err != nil {
		t.Fatalf("status: %v", err)
	}

	if !strings.Contains(output, "not running") && !strings.Contains(output, "Daemon is not running") {
		t.Errorf("expected 'not running' message, got: %q", output)
	}
}

func TestTasksRequiresDaemon(t *testing.T) {
	dir := testProjectWithUpstream(t)

	oldWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()

	rootCmd.SetArgs([]string{"tasks"})
	err := rootCmd.Execute()

	if err == nil {
		t.Fatal("expected error when daemon is not running")
	}
	if !strings.Contains(err.Error(), "daemon is not running") {
		t.Errorf("expected 'daemon is not running' error, got: %v", err)
	}
}

func TestPromptShow(t *testing.T) {
	dir := testProject(t)

	oldWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()

	// Need a git repo for CWD resolution.
	gitExec(t, dir, "init")
	gitExec(t, dir, "config", "user.name", "test")
	gitExec(t, dir, "config", "user.email", "test@test")
	gitExec(t, dir, "add", ".")
	gitExec(t, dir, "commit", "-m", "initial commit")

	// Capture stdout.
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	rootCmd.SetArgs([]string{"prompt", "--show"})
	err := rootCmd.Execute()

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	output := buf.String()

	if err != nil {
		t.Fatalf("prompt --show: %v", err)
	}

	if !strings.Contains(output, "Test Agent Prompt") {
		t.Errorf("expected prompt content, got: %q", output)
	}
}

func TestStartRequiresCredentials(t *testing.T) {
	// Unset credentials so start fails with a clear error.
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")

	rootCmd.SetArgs([]string{"start"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when no credentials are set")
	}
	if !strings.Contains(err.Error(), "no credentials found") {
		t.Errorf("expected 'no credentials found' error, got: %v", err)
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		secs int
		want string
	}{
		{0, "0s"},
		{30, "30s"},
		{60, "1m 0s"},
		{90, "1m 30s"},
		{3600, "1h 0m 0s"},
		{3661, "1h 1m 1s"},
		{7200, "2h 0m 0s"},
		{8130, "2h 15m 30s"},
	}

	for _, tt := range tests {
		got := formatDuration(tt.secs)
		if got != tt.want {
			t.Errorf("formatDuration(%d) = %q, want %q", tt.secs, got, tt.want)
		}
	}
}

func TestFormatRelativeTime(t *testing.T) {
	tests := []struct {
		ago  time.Duration
		want string
	}{
		{10 * time.Second, "10s ago"},
		{5 * time.Minute, "5m ago"},
		{3 * time.Hour, "3h ago"},
		{48 * time.Hour, "2d ago"},
	}

	for _, tt := range tests {
		got := formatRelativeTime(time.Now().Add(-tt.ago))
		if got != tt.want {
			t.Errorf("formatRelativeTime(-%v) = %q, want %q", tt.ago, got, tt.want)
		}
	}
}
