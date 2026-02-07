package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/robmorgan/metamorph/internal/constants"
	"github.com/robmorgan/metamorph/internal/daemon"
	"github.com/robmorgan/metamorph/internal/gitops"
	"github.com/spf13/cobra"
)

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the metamorph daemon and agents",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectDir, err := resolveProjectDir()
		if err != nil {
			return err
		}

		if !daemon.IsRunning(projectDir) {
			return fmt.Errorf("daemon is not running")
		}

		// Read state for summary before stopping.
		state, err := daemon.GetStatus(projectDir)
		if err != nil {
			fmt.Println("Warning: could not read daemon state")
		}

		fmt.Println("Stopping metamorph daemon...")

		if err := daemon.Stop(projectDir); err != nil {
			return fmt.Errorf("failed to stop daemon: %w", err)
		}

		// Sync upstream to working copy (still needed for task file reading).
		upstreamPath := filepath.Join(projectDir, constants.UpstreamDir)
		workingCopyPath := filepath.Join(projectDir, ".metamorph", "work")
		if _, err := gitops.SyncToWorkingCopy(upstreamPath, workingCopyPath); err != nil {
			fmt.Printf("Warning: failed to sync working copy: %v\n", err)
		}

		// Sync agent commits to user's project.
		summary, err := gitops.SyncToProjectDir(upstreamPath, projectDir)
		if err != nil {
			fmt.Printf("Warning: failed to sync to project: %v\n", err)
		} else if summary != "" {
			fmt.Printf("\nSynced commits:\n%s\n", summary)
		}

		fmt.Println("\nDaemon stopped.")

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
