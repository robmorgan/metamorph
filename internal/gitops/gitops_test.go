package gitops

import (
	"fmt"
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

	t.Run("bare repo has expected refs", func(t *testing.T) {
		projectDir := t.TempDir()
		if err := InitUpstream(projectDir); err != nil {
			t.Fatalf("InitUpstream: %v", err)
		}

		upstreamPath := filepath.Join(projectDir, constants.UpstreamDir)

		// Verify refs exist (bare repo should have at least one ref).
		refs, err := git(upstreamPath, "show-ref")
		if err != nil {
			t.Fatalf("show-ref: %v", err)
		}
		if refs == "" {
			t.Error("expected at least one ref in bare repo")
		}
	})

	t.Run("fails on invalid project dir", func(t *testing.T) {
		err := InitUpstream("/nonexistent/path/that/does/not/exist")
		if err == nil {
			t.Fatal("expected error for invalid path")
		}
	})

	t.Run("error includes context", func(t *testing.T) {
		err := InitUpstream("/nonexistent/path")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "gitops:") {
			t.Errorf("error should have gitops prefix: %v", err)
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

	t.Run("produces working clone with correct git remote", func(t *testing.T) {
		_, upstreamPath := setupUpstream(t)

		destDir := filepath.Join(t.TempDir(), "agent-5")
		if err := CloneForAgent(upstreamPath, 5, destDir); err != nil {
			t.Fatalf("CloneForAgent: %v", err)
		}

		// Verify remote points to upstream.
		remote, err := git(destDir, "remote", "get-url", "origin")
		if err != nil {
			t.Fatalf("get remote url: %v", err)
		}
		if remote != upstreamPath {
			t.Errorf("remote = %q, want %q", remote, upstreamPath)
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
			_ = os.RemoveAll(dest)
		}
	})

	t.Run("error includes agent ID context", func(t *testing.T) {
		err := CloneForAgent("/nonexistent/upstream", 42, filepath.Join(t.TempDir(), "dest"))
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "agent-42") {
			t.Errorf("error should mention agent ID: %v", err)
		}
		if !strings.Contains(err.Error(), "gitops:") {
			t.Errorf("error should have gitops prefix: %v", err)
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
		_, _ = git(pusherDir, "config", "user.name", "test")
		_, _ = git(pusherDir, "config", "user.email", "test@test")
		_ = os.WriteFile(filepath.Join(pusherDir, "newfile.txt"), []byte("hello"), 0644)
		_, _ = git(pusherDir, "add", ".")
		_, _ = git(pusherDir, "commit", "-m", "add newfile")
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

	t.Run("pulls multiple new commits", func(t *testing.T) {
		_, upstreamPath := setupUpstream(t)
		wcPath := filepath.Join(t.TempDir(), "wc")

		// Initial sync.
		if _, err := SyncToWorkingCopy(upstreamPath, wcPath); err != nil {
			t.Fatalf("initial sync: %v", err)
		}

		// Push multiple commits.
		pusherDir := filepath.Join(t.TempDir(), "pusher")
		if _, err := git(t.TempDir(), "clone", upstreamPath, pusherDir); err != nil {
			t.Fatalf("clone pusher: %v", err)
		}
		_, _ = git(pusherDir, "config", "user.name", "test")
		_, _ = git(pusherDir, "config", "user.email", "test@test")

		for i := 1; i <= 3; i++ {
			_ = os.WriteFile(filepath.Join(pusherDir, "file"+strings.Repeat("x", i)+".txt"), []byte("data"), 0644)
			_, _ = git(pusherDir, "add", ".")
			_, _ = git(pusherDir, "commit", "-m", "commit "+strings.Repeat("x", i))
		}
		if _, err := git(pusherDir, "push"); err != nil {
			t.Fatalf("push: %v", err)
		}

		summary, err := SyncToWorkingCopy(upstreamPath, wcPath)
		if err != nil {
			t.Fatalf("sync: %v", err)
		}
		// Should contain at least one of the commit messages.
		if summary == "" {
			t.Error("expected non-empty summary")
		}
	})

	t.Run("error wrapping includes context", func(t *testing.T) {
		_, err := SyncToWorkingCopy("/nonexistent/upstream.git", filepath.Join(t.TempDir(), "wc"))
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "gitops:") {
			t.Errorf("error should have gitops prefix: %v", err)
		}
	})

	t.Run("creates parent directory for working copy", func(t *testing.T) {
		_, upstreamPath := setupUpstream(t)
		// Use a path with nested non-existent parents.
		wcPath := filepath.Join(t.TempDir(), "a", "b", "wc")

		summary, err := SyncToWorkingCopy(upstreamPath, wcPath)
		if err != nil {
			t.Fatalf("SyncToWorkingCopy: %v", err)
		}
		if summary == "" {
			t.Error("expected non-empty summary on initial clone")
		}
		if _, err := os.Stat(filepath.Join(wcPath, ".git")); err != nil {
			t.Error(".git dir not found in working copy")
		}
	})
}

func TestCloneForAgent_ConfigErrors(t *testing.T) {
	t.Run("sets user.name and user.email correctly for various IDs", func(t *testing.T) {
		_, upstreamPath := setupUpstream(t)

		for _, id := range []int{1, 10, 100} {
			destDir := filepath.Join(t.TempDir(), "agent")
			if err := CloneForAgent(upstreamPath, id, destDir); err != nil {
				t.Fatalf("CloneForAgent(%d): %v", id, err)
			}
			name, err := git(destDir, "config", "user.name")
			if err != nil {
				t.Fatalf("get user.name: %v", err)
			}
			expected := fmt.Sprintf("agent-%d", id)
			if name != expected {
				t.Errorf("agent %d: user.name = %q, want %q", id, name, expected)
			}
			_ = os.RemoveAll(destDir)
		}
	})
}

func TestInitUpstream_GitInitFailure(t *testing.T) {
	// Create projectDir where upstream.git already exists as a file,
	// so "git init --bare" fails.
	projectDir := t.TempDir()
	metaDir := filepath.Join(projectDir, ".metamorph")
	if err := os.MkdirAll(metaDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Create upstream.git as a regular file — git init --bare will fail.
	if err := os.WriteFile(filepath.Join(metaDir, "upstream.git"), []byte("not a repo"), 0644); err != nil {
		t.Fatal(err)
	}

	err := InitUpstream(projectDir)
	if err == nil {
		t.Fatal("expected error when upstream.git is a file")
	}
	if !strings.Contains(err.Error(), "gitops:") {
		t.Errorf("error should have gitops prefix: %v", err)
	}
}

func TestSyncToWorkingCopy_CorruptedRepo(t *testing.T) {
	// Create a working copy path with a .git that is NOT a valid repo.
	// os.Stat won't return IsNotExist, so SyncToWorkingCopy tries to pull
	// and git rev-parse HEAD fails.
	wcPath := filepath.Join(t.TempDir(), "wc")
	if err := os.MkdirAll(filepath.Join(wcPath, ".git"), 0755); err != nil {
		t.Fatal(err)
	}

	_, err := SyncToWorkingCopy("/some/upstream", wcPath)
	if err == nil {
		t.Fatal("expected error for corrupted .git")
	}
	if !strings.Contains(err.Error(), "gitops:") {
		t.Errorf("error should have gitops prefix: %v", err)
	}
}

func TestSyncToWorkingCopy_MkdirAllFailure(t *testing.T) {
	// Create a read-only directory so MkdirAll fails when creating the parent.
	base := t.TempDir()
	readonlyDir := filepath.Join(base, "readonly")
	if err := os.MkdirAll(readonlyDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Make it read-only so subdirectories can't be created.
	if err := os.Chmod(readonlyDir, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(readonlyDir, 0755) })

	// wcPath = readonly/sub/wc → parent = readonly/sub → MkdirAll fails.
	wcPath := filepath.Join(readonlyDir, "sub", "wc")

	_, err := SyncToWorkingCopy("/some/upstream", wcPath)
	if err == nil {
		t.Fatal("expected error when parent can't be created")
	}
	if !strings.Contains(err.Error(), "gitops:") {
		t.Errorf("error should have gitops prefix: %v", err)
	}
}
