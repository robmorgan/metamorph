package tasks

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const lockDir = "current_tasks"

// TaskLock represents a claimed task.
type TaskLock struct {
	Name      string
	AgentID   int
	ClaimedAt time.Time
}

// git runs a git command in the given directory, capturing stdout and stderr.
func git(dir string, args ...string) (string, string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), err
}

// ClaimTask attempts to claim a task by creating a lock file and pushing.
// Returns true if the claim succeeded, false if another agent got it first.
func ClaimTask(repoDir string, taskName string, agentID int) (bool, error) {
	lockFile := filepath.Join(repoDir, lockDir, taskName+".lock")
	content := fmt.Sprintf("agent-%d %s", agentID, time.Now().UTC().Format(time.RFC3339))

	if err := os.MkdirAll(filepath.Dir(lockFile), 0755); err != nil {
		return false, fmt.Errorf("tasks: failed to create lock dir: %w", err)
	}

	if err := os.WriteFile(lockFile, []byte(content), 0644); err != nil {
		return false, fmt.Errorf("tasks: failed to write lock file: %w", err)
	}

	if _, _, err := git(repoDir, "add", filepath.Join(lockDir, taskName+".lock")); err != nil {
		return false, fmt.Errorf("tasks: failed to stage lock file: %w", err)
	}

	msg := fmt.Sprintf("claim task %s for agent-%d", taskName, agentID)
	if _, _, err := git(repoDir, "commit", "-m", msg); err != nil {
		return false, fmt.Errorf("tasks: failed to commit lock file: %w", err)
	}

	_, stderr, err := git(repoDir, "push")
	if err != nil {
		// Check if this is a push rejection (another agent won the race).
		if strings.Contains(stderr, "rejected") || strings.Contains(stderr, "conflict") {
			// Roll back: remove lock file and reset.
			_ = os.Remove(lockFile)
			_, _, _ = git(repoDir, "checkout", "--", lockDir+"/")
			_, _, _ = git(repoDir, "reset", "--hard", "HEAD~1")
			_, _, _ = git(repoDir, "pull", "--rebase", "origin", "main")
			return false, nil
		}
		return false, fmt.Errorf("tasks: failed to push lock file: %w", err)
	}

	return true, nil
}

// ReleaseTask removes a task lock, verifying this agent owns it.
func ReleaseTask(repoDir string, taskName string, agentID int) error {
	lockFile := filepath.Join(repoDir, lockDir, taskName+".lock")

	data, err := os.ReadFile(lockFile)
	if err != nil {
		return fmt.Errorf("tasks: failed to read lock file: %w", err)
	}

	expectedPrefix := fmt.Sprintf("agent-%d ", agentID)
	if !strings.HasPrefix(string(data), expectedPrefix) {
		return fmt.Errorf("tasks: lock for %q is not owned by agent-%d", taskName, agentID)
	}

	if err := os.Remove(lockFile); err != nil {
		return fmt.Errorf("tasks: failed to remove lock file: %w", err)
	}

	if _, _, err := git(repoDir, "add", filepath.Join(lockDir, taskName+".lock")); err != nil {
		return fmt.Errorf("tasks: failed to stage lock removal: %w", err)
	}

	msg := fmt.Sprintf("release task %s from agent-%d", taskName, agentID)
	if _, _, err := git(repoDir, "commit", "-m", msg); err != nil {
		return fmt.Errorf("tasks: failed to commit lock removal: %w", err)
	}

	if _, _, err := git(repoDir, "push"); err != nil {
		return fmt.Errorf("tasks: failed to push lock removal: %w", err)
	}

	return nil
}

// ListTasks reads all .lock files in current_tasks/ and returns parsed TaskLocks.
func ListTasks(projectDir string) ([]TaskLock, error) {
	dir := filepath.Join(projectDir, lockDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("tasks: failed to read lock dir: %w", err)
	}

	var locks []TaskLock
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".lock") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("tasks: failed to read %s: %w", e.Name(), err)
		}

		lock, err := parseLock(e.Name(), string(data))
		if err != nil {
			return nil, err
		}
		locks = append(locks, lock)
	}

	return locks, nil
}

// ClearStaleTasks removes lock files older than maxAge. Does not git commit —
// the caller decides whether to commit. Returns names of cleared tasks.
func ClearStaleTasks(projectDir string, maxAge time.Duration) ([]string, error) {
	dir := filepath.Join(projectDir, lockDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("tasks: failed to read lock dir: %w", err)
	}

	now := time.Now().UTC()
	var cleared []string

	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".lock") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("tasks: failed to read %s: %w", e.Name(), err)
		}

		lock, err := parseLock(e.Name(), string(data))
		if err != nil {
			return nil, err
		}

		if now.Sub(lock.ClaimedAt) > maxAge {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				return nil, fmt.Errorf("tasks: failed to remove stale lock %s: %w", e.Name(), err)
			}
			cleared = append(cleared, lock.Name)
		}
	}

	return cleared, nil
}

// parseLock parses a lock filename and its content into a TaskLock.
func parseLock(filename, content string) (TaskLock, error) {
	name := strings.TrimSuffix(filename, ".lock")

	parts := strings.SplitN(strings.TrimSpace(content), " ", 2)
	if len(parts) != 2 {
		return TaskLock{}, fmt.Errorf("tasks: malformed lock file %s", filename)
	}

	agentStr := strings.TrimPrefix(parts[0], "agent-")
	agentID, err := strconv.Atoi(agentStr)
	if err != nil {
		return TaskLock{}, fmt.Errorf("tasks: invalid agent ID in %s: %w", filename, err)
	}

	claimedAt, err := time.Parse(time.RFC3339, parts[1])
	if err != nil {
		return TaskLock{}, fmt.Errorf("tasks: invalid timestamp in %s: %w", filename, err)
	}

	return TaskLock{
		Name:      name,
		AgentID:   agentID,
		ClaimedAt: claimedAt,
	}, nil
}
