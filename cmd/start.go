package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"text/tabwriter"

	"github.com/robmorgan/metamorph/internal/broker"
	"github.com/robmorgan/metamorph/internal/config"
	"github.com/robmorgan/metamorph/internal/constants"
	"github.com/robmorgan/metamorph/internal/daemon"
	"github.com/robmorgan/metamorph/internal/gitops"
	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start agents for the current project",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectName, cfg, err := resolveProject(cmd)
		if err != nil {
			return err
		}

		if len(cfg.Repos) == 0 {
			return fmt.Errorf("no repos configured for project %q", projectName)
		}

		// Require a clean working tree for each repo.
		for _, repo := range cfg.Repos {
			label := config.RepoLabel(repo)
			statusCmd := exec.Command("git", "status", "--porcelain")
			statusCmd.Dir = repo.Path
			statusOut, err := statusCmd.Output()
			if err != nil {
				return fmt.Errorf("failed to check git status for repo %q: %w", label, err)
			}
			if len(statusOut) > 0 {
				return fmt.Errorf("uncommitted changes detected in repo %q (%s)\n\nPlease commit your changes first:\n  cd %s && git add -A && git commit -m \"your message\"", label, repo.Path, repo.Path)
			}
		}

		// Create upstream bare repo for each repo that doesn't have one.
		for _, repo := range cfg.Repos {
			label := config.RepoLabel(repo)
			upstreamPath := constants.UpstreamRepoPath(projectName, label)
			if _, err := os.Stat(upstreamPath); os.IsNotExist(err) {
				fmt.Printf("Creating upstream repository for %q...\n", label)
				if err := gitops.InitUpstream(repo.Path, upstreamPath); err != nil {
					return fmt.Errorf("failed to initialize upstream repo for %q: %w", label, err)
				}
			}
		}

		oauthToken := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN")
		apiKey := os.Getenv("ANTHROPIC_API_KEY")
		if oauthToken == "" && apiKey == "" {
			return fmt.Errorf("no credentials found: set CLAUDE_CODE_OAUTH_TOKEN (Claude Pro/Max) or ANTHROPIC_API_KEY")
		}

		dryRun, _ := cmd.Flags().GetBool("dry-run")
		if dryRun {
			fmt.Printf("Project:  %s\n", cfg.Project.Name)
			fmt.Printf("Agents:   %d\n", cfg.Agents.Count)
			fmt.Printf("Model:    %s\n", cfg.Agents.Model)
			fmt.Printf("Roles:    %v\n", cfg.Agents.Roles)
			fmt.Println("\n(dry run -- no agents started)")
			return nil
		}

		// Auto-start daemon if not running.
		if !daemon.IsRunning() {
			fmt.Println("Starting daemon...")
			if err := daemon.Start(apiKey, oauthToken); err != nil {
				return fmt.Errorf("failed to start daemon: %w", err)
			}
		}

		// Set project status to "running" in broker DB so daemon picks it up.
		dbPath := constants.BrokerDBPath()
		store, err := broker.NewSQLiteStore(dbPath)
		if err != nil {
			return fmt.Errorf("failed to open broker database: %w", err)
		}
		defer store.Close()

		ctx := context.Background()
		if err := store.UpdateProjectStatus(ctx, projectName, "running"); err != nil {
			return fmt.Errorf("failed to update project status: %w", err)
		}

		fmt.Printf("Starting agents for %q...\n", projectName)
		fmt.Println("(daemon will start agents on next monitor cycle)")

		// Try to read status after a brief wait.
		state, err := daemon.GetStatus(projectName)
		if err == nil && len(state.Agents) > 0 {
			fmt.Println()
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "AGENT\tROLE\tSTATUS")
			for _, a := range state.Agents {
				_, _ = fmt.Fprintf(w, "agent-%d\t%s\t%s\n", a.ID, a.Role, a.Status)
			}
			_ = w.Flush()
		}

		fmt.Printf("\nUse 'metamorph status' to monitor, 'metamorph stop' to stop.\n")

		return nil
	},
}

func init() {
	startCmd.Flags().IntP("agents", "n", 0, "Number of agents to start (overrides config)")
	startCmd.Flags().String("model", "", "Model to use (overrides config)")
	startCmd.Flags().Bool("dry-run", false, "Print what would happen without starting")
	rootCmd.AddCommand(startCmd)
}
