package cmd

import (
	"context"
	"fmt"

	"github.com/robmorgan/metamorph/internal/broker"
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

		// Sync agent commits to user's project.
		upstreamPath := constants.UpstreamRepoPath(projectName)
		repoPath := cfg.Repos[0].Path

		workingCopyPath := constants.WorkDir(projectName)
		if _, err := gitops.SyncToWorkingCopy(upstreamPath, workingCopyPath); err != nil {
			fmt.Printf("Warning: failed to sync working copy: %v\n", err)
		}

		summary, err := gitops.SyncToProjectDir(upstreamPath, repoPath)
		if err != nil {
			fmt.Printf("Warning: failed to sync to project: %v\n", err)
		} else if summary != "" {
			fmt.Printf("\nSynced commits:\n%s\n", summary)
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
