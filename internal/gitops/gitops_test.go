package gitops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brightfame/metamorph/internal/constants"
)

// setupUpstream creates a temp dir and initializes the upstream bare repo.
func setupUpstream(t *testing.T) (projectDir, upstreamPath string) {
	t.Helper()
	projectDir = t.TempDir()
	if err := InitUpstream(projectDir); err != nil {
		t.Fatalf("InitUpstream: %v", err)
	}
	upstreamPath = filepath.Join(projectDir, constants.UpstreamDir)
	return projectDir, upstreamPath
}

func TestInitUpstream(t *testing.T) {
	t.Run("creates bare repo with seed files", func(t *testing.T) {
		projectDir := t.TempDir()

		if err := InitUpstream(projectDir); err != nil {
			t.Fatalf("InitUpstream: %v", err)
		}

		upstreamPath := filepath.Join(projectDir, constants.UpstreamDir)

		// Verify the bare repo exists (HEAD file is a marker of a git repo).
		if _, err := os.Stat(filepath.Join(upstreamPath, "HEAD")); err != nil {
			t.Fatalf("bare repo HEAD not found: %v", err)
		}

		// Clone and verify seed files exist.
		cloneDir := filepath.Join(t.TempDir(), "verify")
		if _, err := git(t.TempDir(), "clone", upstreamPath, cloneDir); err != nil {
			t.Fatalf("clone for verification: %v", err)
		}

		for _, f := range []string{
			constants.AgentPromptFile,
			constants.ProgressFile,
			filepath.Join(constants.TaskLockDir, ".gitkeep"),
		} {
			if _, err := os.Stat(filepath.Join(cloneDir, f)); err != nil {
				t.Errorf("seed file %s not found: %v", f, err)
			}
		}

		// Verify commit message.
		log, err := git(cloneDir, "log", "--oneline")
		if err != nil {
			t.Fatalf("git log: %v", err)
		}
		if !strings.Contains(log, "metamorph: initial commit") {
			t.Errorf("expected initial commit message, got: %s", log)
		}
	})

	t.Run("fails on invalid project dir", func(t *testing.T) {
		err := InitUpstream("/nonexistent/path/that/does/not/exist")
		if err == nil {
			t.Fatal("expected error for invalid path")
		}
	})
}

func TestCloneForAgent(t *testing.T) {
	t.Run("clones and sets git identity", func(t *testing.T) {
		_, upstreamPath := setupUpstream(t)

		destDir := filepath.Join(t.TempDir(), "agent-1")
		if err := CloneForAgent(upstreamPath, 1, destDir); err != nil {
			t.Fatalf("CloneForAgent: %v", err)
		}

		// Verify clone exists.
		if _, err := os.Stat(filepath.Join(destDir, ".git")); err != nil {
			t.Fatal("clone .git dir not found")
		}

		// Verify git config.
		name, err := git(destDir, "config", "user.name")
		if err != nil {
			t.Fatalf("get user.name: %v", err)
		}
		if name != "agent-1" {
			t.Errorf("user.name = %q, want %q", name, "agent-1")
		}

		email, err := git(destDir, "config", "user.email")
		if err != nil {
			t.Fatalf("get user.email: %v", err)
		}
		if email != "agent-1@metamorph.local" {
			t.Errorf("user.email = %q, want %q", email, "agent-1@metamorph.local")
		}

		// Verify seed files are present.
		if _, err := os.Stat(filepath.Join(destDir, constants.AgentPromptFile)); err != nil {
			t.Error("AGENT_PROMPT.md not found in clone")
		}
	})

	t.Run("multiple agents get independent clones", func(t *testing.T) {
		_, upstreamPath := setupUpstream(t)
		base := t.TempDir()

		for i := 1; i <= 3; i++ {
			dest := filepath.Join(base, "agent")
			if err := CloneForAgent(upstreamPath, i, dest); err != nil {
				t.Fatalf("CloneForAgent(%d): %v", i, err)
			}
			// Verify identity is independent.
			name, _ := git(dest, "config", "user.name")
			if expected := "agent-" + strings.TrimPrefix(name, "agent-"); name != expected {
				t.Errorf("agent %d: unexpected name %q", i, name)
			}
			// Clean up for next iteration since we use the same base name.
			os.RemoveAll(dest)
		}
	})
}

func TestSyncToWorkingCopy(t *testing.T) {
	t.Run("clones when no repo exists", func(t *testing.T) {
		_, upstreamPath := setupUpstream(t)
		wcPath := filepath.Join(t.TempDir(), "wc")

		summary, err := SyncToWorkingCopy(upstreamPath, wcPath)
		if err != nil {
			t.Fatalf("SyncToWorkingCopy (initial): %v", err)
		}

		if !strings.Contains(summary, "metamorph: initial commit") {
			t.Errorf("expected initial commit in summary, got: %q", summary)
		}

		// Verify files.
		if _, err := os.Stat(filepath.Join(wcPath, constants.ProgressFile)); err != nil {
			t.Error("PROGRESS.md not found after sync")
		}
	})

	t.Run("returns empty when already up to date", func(t *testing.T) {
		_, upstreamPath := setupUpstream(t)
		wcPath := filepath.Join(t.TempDir(), "wc")

		// Initial sync.
		if _, err := SyncToWorkingCopy(upstreamPath, wcPath); err != nil {
			t.Fatalf("initial sync: %v", err)
		}

		// Second sync — no changes.
		summary, err := SyncToWorkingCopy(upstreamPath, wcPath)
		if err != nil {
			t.Fatalf("second sync: %v", err)
		}
		if summary != "" {
			t.Errorf("expected empty summary, got: %q", summary)
		}
	})

	t.Run("pulls new commits", func(t *testing.T) {
		_, upstreamPath := setupUpstream(t)
		wcPath := filepath.Join(t.TempDir(), "wc")

		// Initial sync.
		if _, err := SyncToWorkingCopy(upstreamPath, wcPath); err != nil {
			t.Fatalf("initial sync: %v", err)
		}

		// Push a new commit from a separate clone.
		pusherDir := filepath.Join(t.TempDir(), "pusher")
		if _, err := git(t.TempDir(), "clone", upstreamPath, pusherDir); err != nil {
			t.Fatalf("clone pusher: %v", err)
		}
		git(pusherDir, "config", "user.name", "test")
		git(pusherDir, "config", "user.email", "test@test")
		os.WriteFile(filepath.Join(pusherDir, "newfile.txt"), []byte("hello"), 0644)
		git(pusherDir, "add", ".")
		git(pusherDir, "commit", "-m", "add newfile")
		if _, err := git(pusherDir, "push"); err != nil {
			t.Fatalf("push from pusher: %v", err)
		}

		// Sync should pick up the new commit.
		summary, err := SyncToWorkingCopy(upstreamPath, wcPath)
		if err != nil {
			t.Fatalf("sync after push: %v", err)
		}
		if !strings.Contains(summary, "add newfile") {
			t.Errorf("expected 'add newfile' in summary, got: %q", summary)
		}

		// Verify the file arrived.
		if _, err := os.Stat(filepath.Join(wcPath, "newfile.txt")); err != nil {
			t.Error("newfile.txt not found after sync")
		}
	})
}
