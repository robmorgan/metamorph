package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/robmorgan/metamorph/internal/constants"
)

// testProject creates a temp dir with a valid metamorph.toml, AGENT_PROMPT.md,
// and supporting files. Returns the project dir path.
func testProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	configContent := `[project]
name = "test-proj"
description = "Test project"

[agents]
count = 2
model = "claude-sonnet"
roles = ["developer", "tester"]

[docker]
image = "metamorph-agent:latest"

[testing]
command = "go test ./..."

[notifications]
webhook_url = ""
`
	if err := os.WriteFile(filepath.Join(dir, "metamorph.toml"), []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, constants.AgentPromptFile), []byte("# Test Agent Prompt\nHello ${AGENT_ID}"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, constants.ProgressFile), []byte("# Progress\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(dir, constants.TaskLockDir), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, constants.AgentLogDir), 0755); err != nil {
		t.Fatal(err)
	}

	return dir
}

// testProjectWithUpstream creates a testProject, initializes it as a git repo
// with an initial commit, then clones it as a bare upstream repo — matching
// the real InitUpstream behavior (shared history).
func testProjectWithUpstream(t *testing.T) string {
	t.Helper()
	dir := testProject(t)

	// Initialize the project dir as a git repo with an initial commit.
	gitExec(t, dir, "init")
	gitExec(t, dir, "config", "user.name", "test")
	gitExec(t, dir, "config", "user.email", "test@test")
	gitExec(t, dir, "add", ".")
	gitExec(t, dir, "commit", "-m", "initial commit")

	// Clone the project as a bare upstream repo (shared history).
	upstreamPath := filepath.Join(dir, constants.UpstreamDir)
	if err := os.MkdirAll(filepath.Dir(upstreamPath), 0755); err != nil {
		t.Fatal(err)
	}
	gitExec(t, dir, "clone", "--bare", ".", upstreamPath)

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
		gitExec(t, cloneDir, "add", ".")
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

func TestInitCreatesAllFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "new-project")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	gitExec(t, dir, "init")
	gitExec(t, dir, "config", "user.name", "test")
	gitExec(t, dir, "config", "user.email", "test@test")
	// Create a dummy file so we can make an initial commit.
	if err := os.WriteFile(filepath.Join(dir, ".gitkeep"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	gitExec(t, dir, "add", ".")
	gitExec(t, dir, "commit", "-m", "initial commit")

	rootCmd.SetArgs([]string{"init", dir})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("init: %v", err)
	}

	// Check files.
	for _, f := range []string{
		"metamorph.toml",
		constants.AgentPromptFile,
		constants.ProgressFile,
		".gitignore",
	} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("expected %s to exist: %v", f, err)
		}
	}

	// Check directories.
	for _, d := range []string{
		constants.TaskLockDir,
		constants.AgentLogDir,
		constants.UpstreamDir,
	} {
		info, err := os.Stat(filepath.Join(dir, d))
		if err != nil {
			t.Errorf("expected %s to exist: %v", d, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("expected %s to be a directory", d)
		}
	}

	// Verify config content.
	data, err := os.ReadFile(filepath.Join(dir, "metamorph.toml"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	content := string(data)
	projectName := filepath.Base(dir)
	if !strings.Contains(content, projectName) {
		t.Errorf("config should contain project name %q", projectName)
	}
	if !strings.Contains(content, "count = 4") {
		t.Error("config should have default count = 4")
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
}

func TestInitPreservesExistingPrompt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "existing-project")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	gitExec(t, dir, "init")
	gitExec(t, dir, "config", "user.name", "test")
	gitExec(t, dir, "config", "user.email", "test@test")

	// Write a custom AGENT_PROMPT.md before init.
	customContent := "# My Custom Prompt\nDo special things.\n"
	if err := os.WriteFile(filepath.Join(dir, constants.AgentPromptFile), []byte(customContent), 0644); err != nil {
		t.Fatal(err)
	}

	gitExec(t, dir, "add", ".")
	gitExec(t, dir, "commit", "-m", "initial commit")

	rootCmd.SetArgs([]string{"init", dir})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("init: %v", err)
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

func TestInitRejectsExistingProject(t *testing.T) {
	dir := testProject(t)
	gitExec(t, dir, "init")
	gitExec(t, dir, "config", "user.name", "test")
	gitExec(t, dir, "config", "user.email", "test@test")
	gitExec(t, dir, "add", ".")
	gitExec(t, dir, "commit", "-m", "initial commit")

	rootCmd.SetArgs([]string{"init", dir})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for existing project")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestStatusWithoutDaemon(t *testing.T) {
	dir := testProject(t)

	oldWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()

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

func TestTasksWithNoLocks(t *testing.T) {
	dir := testProjectWithUpstream(t)

	oldWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()

	// Capture stdout.
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	rootCmd.SetArgs([]string{"tasks"})
	err := rootCmd.Execute()

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	output := buf.String()

	if err != nil {
		t.Fatalf("tasks: %v", err)
	}

	if !strings.Contains(output, "No active task locks") {
		t.Errorf("expected 'No active task locks' message, got: %q", output)
	}
}

func TestPromptShow(t *testing.T) {
	dir := testProject(t)

	oldWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()

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

func TestStartDryRun(t *testing.T) {
	dir := testProjectWithUpstream(t)

	oldWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()

	// Set a dummy API key so the command doesn't fail on that.
	t.Setenv("ANTHROPIC_API_KEY", "sk-test-dummy")

	// Capture stdout.
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	rootCmd.SetArgs([]string{"start", "--dry-run"})
	err := rootCmd.Execute()

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	output := buf.String()

	if err != nil {
		t.Fatalf("start --dry-run: %v", err)
	}

	if !strings.Contains(output, "test-proj") {
		t.Errorf("expected project name in output, got: %q", output)
	}
	if !strings.Contains(output, "dry run") {
		t.Errorf("expected 'dry run' in output, got: %q", output)
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
