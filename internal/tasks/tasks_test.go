package tasks

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// setupRepo creates a bare upstream repo seeded with current_tasks/.gitkeep,
// and returns the upstream path plus a helper to clone agent worktrees.
func setupRepo(t *testing.T) (upstreamPath string, cloneAgent func(agentID int) string) {
	t.Helper()
	base := t.TempDir()
	upstreamPath = filepath.Join(base, "upstream.git")

	// Create bare repo.
	if _, _, err := git(base, "init", "--bare", upstreamPath); err != nil {
		t.Fatalf("init bare: %v", err)
	}

	// Seed it via a temp clone.
	seedDir := filepath.Join(base, "seed")
	if _, _, err := git(base, "clone", upstreamPath, seedDir); err != nil {
		t.Fatalf("clone for seed: %v", err)
	}
	_, _, _ = git(seedDir, "config", "user.name", "setup")
	_, _, _ = git(seedDir, "config", "user.email", "setup@test")

	taskDir := filepath.Join(seedDir, lockDir)
	_ = os.MkdirAll(taskDir, 0755)
	_ = os.WriteFile(filepath.Join(taskDir, ".gitkeep"), []byte(""), 0644)
	_, _, _ = git(seedDir, "add", ".")
	_, _, _ = git(seedDir, "commit", "-m", "seed")

	branch, _, _ := git(seedDir, "rev-parse", "--abbrev-ref", "HEAD")
	if _, _, err := git(seedDir, "push", "origin", branch); err != nil {
		t.Fatalf("push seed: %v", err)
	}

	cloneAgent = func(agentID int) string {
		dir := filepath.Join(t.TempDir(), fmt.Sprintf("agent-%d", agentID))
		if _, _, err := git(filepath.Dir(dir), "clone", upstreamPath, dir); err != nil {
			t.Fatalf("clone for agent-%d: %v", agentID, err)
		}
		_, _, _ = git(dir, "config", "user.name", fmt.Sprintf("agent-%d", agentID))
		_, _, _ = git(dir, "config", "user.email", fmt.Sprintf("agent-%d@test", agentID))
		return dir
	}

	return upstreamPath, cloneAgent
}

func TestClaimTask(t *testing.T) {
	t.Run("successful claim", func(t *testing.T) {
		_, cloneAgent := setupRepo(t)
		repo := cloneAgent(1)

		claimed, err := ClaimTask(repo, "fix-bug", 1)
		if err != nil {
			t.Fatalf("ClaimTask: %v", err)
		}
		if !claimed {
			t.Fatal("expected claim to succeed")
		}

		// Verify lock file exists.
		lockFile := filepath.Join(repo, lockDir, "fix-bug.lock")
		data, err := os.ReadFile(lockFile)
		if err != nil {
			t.Fatalf("read lock file: %v", err)
		}
		if !strings.HasPrefix(string(data), "agent-1 ") {
			t.Errorf("lock content = %q, want prefix 'agent-1 '", data)
		}

		// Verify it was pushed (check git log).
		log, _, _ := git(repo, "log", "--oneline", "-1")
		if !strings.Contains(log, "claim task fix-bug") {
			t.Errorf("commit message = %q, want 'claim task fix-bug'", log)
		}
	})

	t.Run("race condition: exactly one winner", func(t *testing.T) {
		_, cloneAgent := setupRepo(t)

		repo1 := cloneAgent(1)
		repo2 := cloneAgent(2)

		// Agent 1 claims first.
		claimed1, err1 := ClaimTask(repo1, "shared-task", 1)
		if err1 != nil {
			t.Fatalf("agent-1 ClaimTask: %v", err1)
		}

		// Agent 2 tries to claim the same task — push should be rejected.
		claimed2, err2 := ClaimTask(repo2, "shared-task", 2)
		if err2 != nil {
			t.Fatalf("agent-2 ClaimTask: %v", err2)
		}

		if claimed1 == claimed2 {
			t.Fatalf("expected exactly one winner: agent-1=%v, agent-2=%v", claimed1, claimed2)
		}
		if !claimed1 {
			t.Error("expected agent-1 to win (pushed first)")
		}
		if claimed2 {
			t.Error("expected agent-2 to lose (push rejected)")
		}
	})

	t.Run("concurrent claim: exactly one winner among 5 agents", func(t *testing.T) {
		_, cloneAgent := setupRepo(t)

		const numAgents = 5
		repos := make([]string, numAgents)
		for i := 0; i < numAgents; i++ {
			repos[i] = cloneAgent(i + 1)
		}

		var (
			wg       sync.WaitGroup
			winners  int32
			errCount int32
		)

		wg.Add(numAgents)
		for i := 0; i < numAgents; i++ {
			go func(agentID int, repo string) {
				defer wg.Done()
				claimed, err := ClaimTask(repo, "contested-task", agentID)
				if err != nil {
					atomic.AddInt32(&errCount, 1)
					return
				}
				if claimed {
					atomic.AddInt32(&winners, 1)
				}
			}(i+1, repos[i])
		}
		wg.Wait()

		if winners != 1 {
			t.Errorf("expected exactly 1 winner, got %d (errors: %d)", winners, errCount)
		}
	})
}

func TestReleaseTask(t *testing.T) {
	t.Run("owner can release", func(t *testing.T) {
		_, cloneAgent := setupRepo(t)
		repo := cloneAgent(1)

		claimed, err := ClaimTask(repo, "my-task", 1)
		if err != nil || !claimed {
			t.Fatalf("ClaimTask: claimed=%v, err=%v", claimed, err)
		}

		if err := ReleaseTask(repo, "my-task", 1); err != nil {
			t.Fatalf("ReleaseTask: %v", err)
		}

		// Verify lock file is gone.
		lockFile := filepath.Join(repo, lockDir, "my-task.lock")
		if _, err := os.Stat(lockFile); !os.IsNotExist(err) {
			t.Error("lock file should be removed after release")
		}
	})

	t.Run("non-owner cannot release", func(t *testing.T) {
		_, cloneAgent := setupRepo(t)
		repo := cloneAgent(1)

		claimed, err := ClaimTask(repo, "guarded-task", 1)
		if err != nil || !claimed {
			t.Fatalf("ClaimTask: claimed=%v, err=%v", claimed, err)
		}

		// Agent 2 tries to release agent 1's lock.
		err = ReleaseTask(repo, "guarded-task", 2)
		if err == nil {
			t.Fatal("expected error when non-owner tries to release")
		}
		if !strings.Contains(err.Error(), "not owned by agent-2") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("release nonexistent task", func(t *testing.T) {
		_, cloneAgent := setupRepo(t)
		repo := cloneAgent(1)

		err := ReleaseTask(repo, "no-such-task", 1)
		if err == nil {
			t.Fatal("expected error for nonexistent task")
		}
	})
}

func TestListTasks(t *testing.T) {
	t.Run("empty list", func(t *testing.T) {
		_, cloneAgent := setupRepo(t)
		repo := cloneAgent(1)

		locks, err := ListTasks(repo)
		if err != nil {
			t.Fatalf("ListTasks: %v", err)
		}
		if len(locks) != 0 {
			t.Errorf("expected 0 locks, got %d", len(locks))
		}
	})

	t.Run("lists claimed tasks", func(t *testing.T) {
		_, cloneAgent := setupRepo(t)
		repo := cloneAgent(1)

		_, _ = ClaimTask(repo, "task-a", 1)
		_, _ = ClaimTask(repo, "task-b", 1)

		locks, err := ListTasks(repo)
		if err != nil {
			t.Fatalf("ListTasks: %v", err)
		}
		if len(locks) != 2 {
			t.Fatalf("expected 2 locks, got %d", len(locks))
		}

		names := map[string]bool{}
		for _, l := range locks {
			names[l.Name] = true
			if l.AgentID != 1 {
				t.Errorf("lock %s: agentID = %d, want 1", l.Name, l.AgentID)
			}
			if l.ClaimedAt.IsZero() {
				t.Errorf("lock %s: ClaimedAt is zero", l.Name)
			}
		}
		if !names["task-a"] || !names["task-b"] {
			t.Errorf("expected task-a and task-b, got %v", names)
		}
	})

	t.Run("ignores non-lock files", func(t *testing.T) {
		_, cloneAgent := setupRepo(t)
		repo := cloneAgent(1)

		// .gitkeep already exists — should be ignored.
		locks, err := ListTasks(repo)
		if err != nil {
			t.Fatalf("ListTasks: %v", err)
		}
		if len(locks) != 0 {
			t.Errorf("expected 0 locks (only .gitkeep), got %d", len(locks))
		}
	})

	t.Run("missing dir returns nil", func(t *testing.T) {
		locks, err := ListTasks("/nonexistent/dir")
		if err != nil {
			t.Fatalf("ListTasks: %v", err)
		}
		if locks != nil {
			t.Errorf("expected nil, got %v", locks)
		}
	})

	t.Run("returns all active locks with correct parsing", func(t *testing.T) {
		dir := t.TempDir()
		taskDir := filepath.Join(dir, lockDir)
		_ = os.MkdirAll(taskDir, 0755)

		ts := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
		_ = os.WriteFile(
			filepath.Join(taskDir, "task-alpha.lock"),
			[]byte(fmt.Sprintf("agent-3 %s", ts.Format(time.RFC3339))),
			0644,
		)
		_ = os.WriteFile(
			filepath.Join(taskDir, "task-beta.lock"),
			[]byte(fmt.Sprintf("agent-7 %s", ts.Add(time.Hour).Format(time.RFC3339))),
			0644,
		)

		locks, err := ListTasks(dir)
		if err != nil {
			t.Fatalf("ListTasks: %v", err)
		}
		if len(locks) != 2 {
			t.Fatalf("expected 2 locks, got %d", len(locks))
		}

		for _, l := range locks {
			switch l.Name {
			case "task-alpha":
				if l.AgentID != 3 {
					t.Errorf("task-alpha AgentID = %d, want 3", l.AgentID)
				}
				if !l.ClaimedAt.Equal(ts) {
					t.Errorf("task-alpha ClaimedAt = %v, want %v", l.ClaimedAt, ts)
				}
			case "task-beta":
				if l.AgentID != 7 {
					t.Errorf("task-beta AgentID = %d, want 7", l.AgentID)
				}
			default:
				t.Errorf("unexpected lock: %s", l.Name)
			}
		}
	})
}

func TestClearStaleTasks(t *testing.T) {
	t.Run("clears old locks", func(t *testing.T) {
		dir := t.TempDir()
		taskDir := filepath.Join(dir, lockDir)
		_ = os.MkdirAll(taskDir, 0755)

		// Write a lock file with a timestamp 2 hours ago.
		old := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
		_ = os.WriteFile(
			filepath.Join(taskDir, "old-task.lock"),
			[]byte(fmt.Sprintf("agent-1 %s", old)),
			0644,
		)

		// Write a lock file with a recent timestamp.
		recent := time.Now().UTC().Format(time.RFC3339)
		_ = os.WriteFile(
			filepath.Join(taskDir, "new-task.lock"),
			[]byte(fmt.Sprintf("agent-2 %s", recent)),
			0644,
		)

		cleared, err := ClearStaleTasks(dir, 1*time.Hour)
		if err != nil {
			t.Fatalf("ClearStaleTasks: %v", err)
		}

		if len(cleared) != 1 || cleared[0] != "old-task" {
			t.Errorf("expected [old-task], got %v", cleared)
		}

		// Verify old-task.lock is gone.
		if _, err := os.Stat(filepath.Join(taskDir, "old-task.lock")); !os.IsNotExist(err) {
			t.Error("old-task.lock should be removed")
		}

		// Verify new-task.lock still exists.
		if _, err := os.Stat(filepath.Join(taskDir, "new-task.lock")); err != nil {
			t.Error("new-task.lock should still exist")
		}
	})

	t.Run("nothing to clear", func(t *testing.T) {
		dir := t.TempDir()
		taskDir := filepath.Join(dir, lockDir)
		_ = os.MkdirAll(taskDir, 0755)

		recent := time.Now().UTC().Format(time.RFC3339)
		_ = os.WriteFile(
			filepath.Join(taskDir, "fresh.lock"),
			[]byte(fmt.Sprintf("agent-1 %s", recent)),
			0644,
		)

		cleared, err := ClearStaleTasks(dir, 1*time.Hour)
		if err != nil {
			t.Fatalf("ClearStaleTasks: %v", err)
		}
		if len(cleared) != 0 {
			t.Errorf("expected no cleared tasks, got %v", cleared)
		}
	})

	t.Run("missing dir returns nil", func(t *testing.T) {
		cleared, err := ClearStaleTasks("/nonexistent/dir", time.Hour)
		if err != nil {
			t.Fatalf("ClearStaleTasks: %v", err)
		}
		if cleared != nil {
			t.Errorf("expected nil, got %v", cleared)
		}
	})

	t.Run("only removes old locks not recent ones", func(t *testing.T) {
		dir := t.TempDir()
		taskDir := filepath.Join(dir, lockDir)
		_ = os.MkdirAll(taskDir, 0755)

		// 3 old, 2 recent.
		for i := 1; i <= 3; i++ {
			ts := time.Now().UTC().Add(-3 * time.Hour).Format(time.RFC3339)
			_ = os.WriteFile(
				filepath.Join(taskDir, fmt.Sprintf("old-%d.lock", i)),
				[]byte(fmt.Sprintf("agent-%d %s", i, ts)),
				0644,
			)
		}
		for i := 1; i <= 2; i++ {
			ts := time.Now().UTC().Format(time.RFC3339)
			_ = os.WriteFile(
				filepath.Join(taskDir, fmt.Sprintf("new-%d.lock", i)),
				[]byte(fmt.Sprintf("agent-%d %s", i, ts)),
				0644,
			)
		}

		cleared, err := ClearStaleTasks(dir, 1*time.Hour)
		if err != nil {
			t.Fatalf("ClearStaleTasks: %v", err)
		}
		if len(cleared) != 3 {
			t.Errorf("expected 3 cleared, got %d: %v", len(cleared), cleared)
		}

		// Verify new locks still exist.
		remaining, _ := ListTasks(dir)
		if len(remaining) != 2 {
			t.Errorf("expected 2 remaining, got %d", len(remaining))
		}
	})
}

func TestParseLock(t *testing.T) {
	t.Run("valid lock", func(t *testing.T) {
		lock, err := parseLock("fix-bug.lock", "agent-3 2025-06-15T10:30:00Z")
		if err != nil {
			t.Fatalf("parseLock: %v", err)
		}
		if lock.Name != "fix-bug" {
			t.Errorf("Name = %q", lock.Name)
		}
		if lock.AgentID != 3 {
			t.Errorf("AgentID = %d", lock.AgentID)
		}
		expected := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
		if !lock.ClaimedAt.Equal(expected) {
			t.Errorf("ClaimedAt = %v, want %v", lock.ClaimedAt, expected)
		}
	})

	t.Run("malformed content", func(t *testing.T) {
		_, err := parseLock("bad.lock", "no-space-here")
		if err == nil {
			t.Fatal("expected error for malformed content")
		}
		if !strings.Contains(err.Error(), "malformed lock file") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("invalid agent ID", func(t *testing.T) {
		_, err := parseLock("bad.lock", "agent-xyz 2025-06-15T10:30:00Z")
		if err == nil {
			t.Fatal("expected error for invalid agent ID")
		}
		if !strings.Contains(err.Error(), "invalid agent ID") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("invalid timestamp", func(t *testing.T) {
		_, err := parseLock("bad.lock", "agent-1 not-a-timestamp")
		if err == nil {
			t.Fatal("expected error for invalid timestamp")
		}
		if !strings.Contains(err.Error(), "invalid timestamp") {
			t.Errorf("unexpected error: %v", err)
		}
	})
}
