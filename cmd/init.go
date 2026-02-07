package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/robmorgan/metamorph/assets"
	"github.com/robmorgan/metamorph/internal/constants"
	"github.com/robmorgan/metamorph/internal/gitops"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init [directory]",
	Short: "Initialize a new metamorph project",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir := "."
		if len(args) > 0 {
			dir = args[0]
		}

		absDir, err := filepath.Abs(dir)
		if err != nil {
			return fmt.Errorf("failed to resolve directory: %w", err)
		}
		projectName := filepath.Base(absDir)

		// Ensure the target directory exists.
		if err := os.MkdirAll(absDir, 0755); err != nil {
			return fmt.Errorf("failed to create directory: %w", err)
		}

		// Check if already initialized.
		configPath := filepath.Join(absDir, "metamorph.toml")
		if _, err := os.Stat(configPath); err == nil {
			return fmt.Errorf("metamorph.toml already exists in %s", absDir)
		}

		// Write metamorph.toml.
		configContent := fmt.Sprintf(`[project]
name = %q
description = ""

[agents]
count = 4
model = "claude-opus-4-6"
roles = ["developer", "developer", "tester", "refactorer"]

[docker]
image = "metamorph-agent:latest"
extra_packages = []

[testing]
command = ""
fast_command = ""

[notifications]
webhook_url = ""

[git]
author_name = ""
author_email = ""
`, projectName)

		if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
			return fmt.Errorf("failed to write metamorph.toml: %w", err)
		}
		fmt.Println("  Created metamorph.toml")

		// Write AGENT_PROMPT.md from embedded template.
		agentPromptPath := filepath.Join(absDir, constants.AgentPromptFile)
		if err := os.WriteFile(agentPromptPath, []byte(assets.DefaultAgentPrompt), 0644); err != nil {
			return fmt.Errorf("failed to write AGENT_PROMPT.md: %w", err)
		}
		fmt.Println("  Created AGENT_PROMPT.md")

		// Write PROGRESS.md.
		progressPath := filepath.Join(absDir, constants.ProgressFile)
		progressContent := `# Progress

## Completed

## In Progress

## Blocked

## Notes
`

		if err := os.WriteFile(progressPath, []byte(progressContent), 0644); err != nil {
			return fmt.Errorf("failed to write PROGRESS.md: %w", err)
		}
		fmt.Println("  Created PROGRESS.md")

		// Create directories.
		for _, d := range []string{constants.TaskLockDir, constants.AgentLogDir} {
			if err := os.MkdirAll(filepath.Join(absDir, d), 0755); err != nil {
				return fmt.Errorf("failed to create %s: %w", d, err)
			}
			fmt.Printf("  Created %s/\n", d)
		}

		// Initialize upstream bare repo.
		if err := gitops.InitUpstream(absDir); err != nil {
			return fmt.Errorf("failed to initialize upstream repo: %w", err)
		}
		fmt.Println("  Initialized upstream repo")

		// Update upstream with full content: clone to temp, overwrite files, commit, push.
		upstreamPath := filepath.Join(absDir, constants.UpstreamDir)
		if err := syncFilesToUpstream(absDir, upstreamPath); err != nil {
			return fmt.Errorf("failed to sync files to upstream: %w", err)
		}
		fmt.Println("  Synced project files to upstream")

		// Write .gitignore.
		gitignorePath := filepath.Join(absDir, ".gitignore")
		gitignoreContent := ".metamorph/\nagent_logs/\n"
		if err := os.WriteFile(gitignorePath, []byte(gitignoreContent), 0644); err != nil {
			return fmt.Errorf("failed to write .gitignore: %w", err)
		}
		fmt.Println("  Created .gitignore")

		fmt.Printf("\nProject %q initialized successfully!\n\n", projectName)
		fmt.Println("Next steps:")
		fmt.Println("  1. Review and customize metamorph.toml")
		fmt.Println("  2. Edit AGENT_PROMPT.md with project-specific instructions")
		fmt.Println("  3. Set credentials (pick one):")
		fmt.Println("       export CLAUDE_CODE_OAUTH_TOKEN=...   # Claude Pro/Max subscription")
		fmt.Println("       export ANTHROPIC_API_KEY=sk-...       # Anthropic API key")
		fmt.Println("  4. Start agents: metamorph start")

		return nil
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
}

// syncFilesToUpstream clones the upstream bare repo to a temp dir, copies project
// files in, commits, and pushes.
func syncFilesToUpstream(projectDir, upstreamPath string) error {
	tmpDir, err := os.MkdirTemp("", "metamorph-sync-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	cloneDir := filepath.Join(tmpDir, "work")

	// Clone.
	if err := runGit(tmpDir, "clone", upstreamPath, cloneDir); err != nil {
		return fmt.Errorf("failed to clone upstream: %w", err)
	}

	// Copy project files into the clone.
	filesToSync := []string{"metamorph.toml", constants.AgentPromptFile, constants.ProgressFile}
	for _, name := range filesToSync {
		src := filepath.Join(projectDir, name)
		dst := filepath.Join(cloneDir, name)
		data, err := os.ReadFile(src)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", name, err)
		}
		if err := os.WriteFile(dst, data, 0644); err != nil {
			return fmt.Errorf("failed to write %s: %w", name, err)
		}
	}

	// Set identity so commits work without a global git config.
	_ = runGit(cloneDir, "config", "user.name", "metamorph")
	_ = runGit(cloneDir, "config", "user.email", "metamorph@localhost")

	// Stage and commit.
	if err := runGit(cloneDir, "add", "."); err != nil {
		return fmt.Errorf("failed to stage files: %w", err)
	}
	if err := runGit(cloneDir, "commit", "-m", "metamorph: sync project files"); err != nil {
		return fmt.Errorf("failed to commit: %w", err)
	}

	// Detect branch and push.
	branch, err := runGitOutput(cloneDir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return fmt.Errorf("failed to detect branch: %w", err)
	}
	if err := runGit(cloneDir, "push", "origin", branch); err != nil {
		return fmt.Errorf("failed to push: %w", err)
	}

	return nil
}

// runGit executes a git command in the given directory, discarding output.
func runGit(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}

// runGitOutput executes a git command and returns trimmed stdout.
func runGitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out[:len(out)-1]), nil // trim trailing newline
}
