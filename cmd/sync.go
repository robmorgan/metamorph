package cmd

import (
	"fmt"

	"github.com/robmorgan/metamorph/internal/constants"
	"github.com/robmorgan/metamorph/internal/gitops"
	"github.com/spf13/cobra"
)

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Sync changes between upstream and agent worktrees",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectName, cfg, err := resolveProject(cmd)
		if err != nil {
			return err
		}

		if len(cfg.Repos) == 0 {
			return fmt.Errorf("no repos configured for project %q", projectName)
		}

		upstreamPath := constants.UpstreamRepoPath(projectName)
		workingCopyPath := constants.WorkDir(projectName)
		repoPath := cfg.Repos[0].Path

		// Sync upstream to working copy (for task file reading).
		if _, err := gitops.SyncToWorkingCopy(upstreamPath, workingCopyPath); err != nil {
			fmt.Printf("Warning: failed to sync working copy: %v\n", err)
		}

		// Sync agent commits to user's project.
		summary, err := gitops.SyncToProjectDir(upstreamPath, repoPath)
		if err != nil {
			return fmt.Errorf("sync failed: %w", err)
		}

		if summary == "" {
			fmt.Println("Already up to date.")
		} else {
			fmt.Println(summary)
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(syncCmd)
}
