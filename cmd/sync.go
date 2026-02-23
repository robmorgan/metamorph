package cmd

import (
	"fmt"

	"github.com/robmorgan/metamorph/internal/config"
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

		allUpToDate := true
		for _, repo := range cfg.Repos {
			label := config.RepoLabel(repo)
			upstreamPath := constants.UpstreamRepoPath(projectName, label)
			workingCopyPath := constants.WorkDir(projectName)

			// Sync upstream to working copy (for task file reading).
			if _, err := gitops.SyncToWorkingCopy(upstreamPath, workingCopyPath); err != nil {
				fmt.Printf("Warning: failed to sync working copy for %q: %v\n", label, err)
			}

			// Sync agent commits to user's project.
			summary, err := gitops.SyncToProjectDir(upstreamPath, repo.Path)
			if err != nil {
				return fmt.Errorf("sync failed for repo %q: %w", label, err)
			}

			if summary != "" {
				allUpToDate = false
				fmt.Printf("[%s] %s\n", label, summary)
			}
		}

		if allUpToDate {
			fmt.Println("Already up to date.")
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(syncCmd)
}
