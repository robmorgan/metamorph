package gitops

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/robmorgan/metamorph/internal/constants"
)

// git runs a git command in the given directory, capturing stdout and stderr.
func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// InitUpstream creates a bare git repo at <projectDir>/.metamorph/upstream.git,
// seeds it with initial files, and pushes an initial commit.
func InitUpstream(projectDir string) error {
	upstreamPath := filepath.Join(projectDir, constants.UpstreamDir)

	if err := os.MkdirAll(filepath.Dir(upstreamPath), 0755); err != nil {
		return fmt.Errorf("gitops: failed to create parent dir: %w", err)
	}

	if _, err := git(projectDir, "init", "--bare", upstreamPath); err != nil {
		return fmt.Errorf("gitops: failed to init bare repo: %w", err)
	}

	// Create a temporary clone to seed the bare repo.
	tmpDir, err := os.MkdirTemp("", "metamorph-init-*")
	if err != nil {
		return fmt.Errorf("gitops: failed to create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	if _, err := git(tmpDir, "clone", upstreamPath, "seed"); err != nil {
		return fmt.Errorf("gitops: failed to clone for seeding: %w", err)
	}

	seedDir := filepath.Join(tmpDir, "seed")

	// Create seed files.
	files := map[string]string{
		constants.ProgressFile:                            "# Progress\n",
		filepath.Join(constants.TaskLockDir, ".gitkeep"): "",
	}
	for relPath, content := range files {
		fullPath := filepath.Join(seedDir, relPath)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			return fmt.Errorf("gitops: failed to create dir for %s: %w", relPath, err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			return fmt.Errorf("gitops: failed to write %s: %w", relPath, err)
		}
	}

	// Set identity for the seed commit so it works in environments without
	// a global git config (e.g. CI runners).
	if _, err := git(seedDir, "config", "user.name", "metamorph"); err != nil {
		return fmt.Errorf("gitops: failed to set user.name in seed clone: %w", err)
	}
	if _, err := git(seedDir, "config", "user.email", "metamorph@localhost"); err != nil {
		return fmt.Errorf("gitops: failed to set user.email in seed clone: %w", err)
	}

	if _, err := git(seedDir, "add", "."); err != nil {
		return fmt.Errorf("gitops: failed to stage seed files: %w", err)
	}

	if _, err := git(seedDir, "commit", "-m", "metamorph: initial commit"); err != nil {
		return fmt.Errorf("gitops: failed to commit seed files: %w", err)
	}

	// Detect branch name and push.
	branch, err := git(seedDir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return fmt.Errorf("gitops: failed to detect branch name: %w", err)
	}

	if _, err := git(seedDir, "push", "origin", branch); err != nil {
		return fmt.Errorf("gitops: failed to push seed commit: %w", err)
	}

	return nil
}

// CloneForAgent clones the upstream repo and configures git identity for the agent.
func CloneForAgent(upstreamPath string, agentID int, destDir string) error {
	parent := filepath.Dir(destDir)
	if _, err := git(parent, "clone", upstreamPath, destDir); err != nil {
		return fmt.Errorf("gitops: failed to clone for agent-%d: %w", agentID, err)
	}

	name := fmt.Sprintf("agent-%d", agentID)
	email := fmt.Sprintf("agent-%d@metamorph.local", agentID)

	if _, err := git(destDir, "config", "user.name", name); err != nil {
		return fmt.Errorf("gitops: failed to set user.name for agent-%d: %w", agentID, err)
	}
	if _, err := git(destDir, "config", "user.email", email); err != nil {
		return fmt.Errorf("gitops: failed to set user.email for agent-%d: %w", agentID, err)
	}

	return nil
}

// SyncToWorkingCopy clones or pulls latest changes into workingCopyPath.
// Returns a summary of new commits.
func SyncToWorkingCopy(upstreamPath string, workingCopyPath string) (string, error) {
	gitDir := filepath.Join(workingCopyPath, ".git")

	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		// No repo yet — clone.
		parent := filepath.Dir(workingCopyPath)
		if err := os.MkdirAll(parent, 0755); err != nil {
			return "", fmt.Errorf("gitops: failed to create parent for working copy: %w", err)
		}
		if _, err := git(parent, "clone", upstreamPath, workingCopyPath); err != nil {
			return "", fmt.Errorf("gitops: failed to clone into working copy: %w", err)
		}
		// Return all commits as the summary.
		summary, err := git(workingCopyPath, "log", "--oneline")
		if err != nil {
			return "", fmt.Errorf("gitops: failed to read log after clone: %w", err)
		}
		return summary, nil
	}

	// Record HEAD before pull.
	oldHead, err := git(workingCopyPath, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("gitops: failed to get HEAD before sync: %w", err)
	}

	branch, err := git(workingCopyPath, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", fmt.Errorf("gitops: failed to detect branch: %w", err)
	}

	if _, err := git(workingCopyPath, "pull", "--rebase", "origin", branch); err != nil {
		return "", fmt.Errorf("gitops: failed to pull --rebase: %w", err)
	}

	newHead, err := git(workingCopyPath, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("gitops: failed to get HEAD after sync: %w", err)
	}

	if oldHead == newHead {
		return "", nil
	}

	summary, err := git(workingCopyPath, "log", "--oneline", oldHead+".."+newHead)
	if err != nil {
		return "", fmt.Errorf("gitops: failed to read new commits: %w", err)
	}
	return summary, nil
}
