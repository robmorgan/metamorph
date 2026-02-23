package cmd

import (
	"context"
	"fmt"

	"github.com/robmorgan/metamorph/internal/broker"
	"github.com/robmorgan/metamorph/internal/config"
	"github.com/robmorgan/metamorph/internal/constants"
	"github.com/robmorgan/metamorph/internal/daemon"
	"github.com/robmorgan/metamorph/internal/gitops"
	"github.com/spf13/cobra"
)

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop agents for the current project",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectName, cfg, err := resolveProject(cmd)
		if err != nil {
			return err
		}

		if !daemon.IsRunning() {
			return fmt.Errorf("daemon is not running")
		}

		// Read state for summary before stopping.
		state, err := daemon.GetStatus(projectName)
		if err != nil {
			fmt.Println("Warning: could not read project state")
		}

		// Set project status to "stopped" in broker DB.
		dbPath := constants.BrokerDBPath()
		store, err := broker.NewSQLiteStore(dbPath)
		if err != nil {
			return fmt.Errorf("failed to open broker database: %w", err)
		}
		defer store.Close()

		ctx := context.Background()
		if err := store.UpdateProjectStatus(ctx, projectName, "stopped"); err != nil {
			return fmt.Errorf("failed to update project status: %w", err)
		}

		fmt.Printf("Stopping agents for %q...\n", projectName)

		// Sync agent commits to user's project for each repo.
		for _, repo := range cfg.Repos {
			label := config.RepoLabel(repo)
			upstreamPath := constants.UpstreamRepoPath(projectName, label)

			workingCopyPath := constants.WorkDir(projectName)
			if _, err := gitops.SyncToWorkingCopy(upstreamPath, workingCopyPath); err != nil {
				fmt.Printf("Warning: failed to sync working copy for %q: %v\n", label, err)
			}

			summary, err := gitops.SyncToProjectDir(upstreamPath, repo.Path)
			if err != nil {
				fmt.Printf("Warning: failed to sync repo %q: %v\n", label, err)
			} else if summary != "" {
				fmt.Printf("\nSynced commits for %q:\n%s\n", label, summary)
			}
		}

		fmt.Println("\nAgents stopped.")

		if state != nil {
			fmt.Printf("\nSession summary:\n")
			fmt.Printf("  Sessions:        %d\n", state.Stats.TotalSessions)
			fmt.Printf("  Commits:         %d\n", state.Stats.TotalCommits)
			fmt.Printf("  Tasks completed: %d\n", state.Stats.TasksCompleted)
			fmt.Printf("  Uptime:          %s\n", formatDuration(state.Stats.UptimeSeconds))
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(stopCmd)
}
