package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/robmorgan/metamorph/internal/broker"
	"github.com/robmorgan/metamorph/internal/config"
	"github.com/robmorgan/metamorph/internal/constants"
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

		// Require the directory to already be a git repo.
		if _, err := os.Stat(filepath.Join(absDir, ".git")); os.IsNotExist(err) {
			return fmt.Errorf("directory is not a git repository: run 'git init' first")
		}

		// Check if project config already exists under ~/.metamorph/projects/{name}/.
		projectDir := constants.ProjectDir(projectName)
		configPath := filepath.Join(projectDir, constants.ProjectConfigFile)
		if _, err := os.Stat(configPath); err == nil {
			return fmt.Errorf("project %q already exists (config at %s)", projectName, configPath)
		}

		// Create project config directory.
		if err := os.MkdirAll(projectDir, 0755); err != nil {
			return fmt.Errorf("failed to create project dir: %w", err)
		}

		// Write config.json under ~/.metamorph/projects/{name}/.
		cfg := &config.Config{
			Project: config.ProjectConfig{
				Name:        projectName,
				Description: "",
			},
			Repos: []config.RepoConfig{
				{Path: absDir, Label: ""},
			},
			Agents: config.AgentsConfig{
				Count: 4,
				Model: "claude-opus-4-6",
				Roles: []string{"developer", "developer", "tester", "refactorer"},
			},
			Docker: config.DockerConfig{
				Image:         "metamorph-agent:latest",
				ExtraPackages: []string{},
			},
			Testing: config.TestingConfig{
				Command:     "",
				FastCommand: "",
			},
			Notifications: config.NotificationsConfig{
				WebhookURL: "",
			},
		}
		if err := config.Save(cfg, configPath); err != nil {
			return fmt.Errorf("failed to write config: %w", err)
		}
		fmt.Printf("  Created %s\n", configPath)

		// Register project in SQLite.
		dbPath := constants.BrokerDBPath()
		if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
			return fmt.Errorf("failed to create db dir: %w", err)
		}
		store, err := broker.NewSQLiteStore(dbPath)
		if err != nil {
			return fmt.Errorf("failed to open broker database: %w", err)
		}
		defer store.Close()

		ctx := context.Background()
		if err := store.RegisterProject(ctx, projectName); err != nil {
			return fmt.Errorf("failed to register project: %w", err)
		}
		for _, repo := range cfg.Repos {
			label := config.RepoLabel(repo)
			if err := store.AddProjectRepo(ctx, projectName, repo.Path, label); err != nil {
				return fmt.Errorf("failed to register repo %q: %w", label, err)
			}
		}
		fmt.Println("  Registered project in broker database")

		// Write AGENT_PROMPT.md skeleton only if it doesn't already exist.
		agentPromptPath := filepath.Join(absDir, constants.AgentPromptFile)
		if _, err := os.Stat(agentPromptPath); os.IsNotExist(err) {
			skeleton := `# Project Instructions

Add project-specific instructions for your agents here.

## Build & Test
<!-- e.g., cargo test, go test ./... -->

## Architecture
<!-- Describe key files and project structure -->

## Task List
<!-- List tasks for agents to work on -->
`
			if err := os.WriteFile(agentPromptPath, []byte(skeleton), 0644); err != nil {
				return fmt.Errorf("failed to write AGENT_PROMPT.md: %w", err)
			}
			fmt.Println("  Created AGENT_PROMPT.md")
		} else {
			fmt.Println("  Using existing AGENT_PROMPT.md")
		}

		// Write PROGRESS.md only if it doesn't already exist.
		progressPath := filepath.Join(absDir, constants.ProgressFile)
		if _, err := os.Stat(progressPath); os.IsNotExist(err) {
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
		} else {
			fmt.Println("  Using existing PROGRESS.md")
		}

		// Create task lock directory in repo.
		if err := os.MkdirAll(filepath.Join(absDir, constants.TaskLockDir), 0755); err != nil {
			return fmt.Errorf("failed to create %s: %w", constants.TaskLockDir, err)
		}
		fmt.Printf("  Created %s/\n", constants.TaskLockDir)

		// Append entries to .gitignore (create if missing, never overwrite).
		gitignorePath := filepath.Join(absDir, ".gitignore")
		requiredEntries := []string{"current_tasks/"}
		existing, _ := os.ReadFile(gitignorePath)
		existingStr := string(existing)
		var toAdd []string
		for _, entry := range requiredEntries {
			if !strings.Contains(existingStr, entry) {
				toAdd = append(toAdd, entry)
			}
		}
		if len(toAdd) > 0 {
			f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				return fmt.Errorf("failed to open .gitignore: %w", err)
			}
			if len(existingStr) > 0 && !strings.HasSuffix(existingStr, "\n") {
				_, _ = f.WriteString("\n")
			}
			for _, entry := range toAdd {
				if _, err := f.WriteString(entry + "\n"); err != nil {
					_ = f.Close()
					return fmt.Errorf("failed to write .gitignore entry: %w", err)
				}
			}
			_ = f.Close()
			fmt.Println("  Updated .gitignore")
		} else {
			fmt.Println("  .gitignore already up to date")
		}

		fmt.Printf("\nProject %q initialized successfully!\n\n", projectName)
		fmt.Println("Next steps:")
		fmt.Printf("  1. Review and customize %s\n", configPath)
		fmt.Println("  2. Edit AGENT_PROMPT.md with project-specific instructions")
		fmt.Println("  3. Commit the changes:")
		fmt.Println("       git add -A && git commit -m \"Initialize metamorph\"")
		fmt.Println("  4. Set credentials (pick one):")
		fmt.Println("       export CLAUDE_CODE_OAUTH_TOKEN=...   # Claude Pro/Max subscription")
		fmt.Println("       export ANTHROPIC_API_KEY=sk-...       # Anthropic API key")
		fmt.Println("  5. Start agents: metamorph start")

		return nil
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
}
