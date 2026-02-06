package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/brightfame/metamorph/internal/constants"
	"github.com/brightfame/metamorph/internal/gitops"
	"github.com/spf13/cobra"
)

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Sync changes between upstream and agent worktrees",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectDir, err := resolveProjectDir()
		if err != nil {
			return err
		}

		upstreamPath := filepath.Join(projectDir, constants.UpstreamDir)
		workingCopyPath := filepath.Join(projectDir, ".metamorph", "work")

		summary, err := gitops.SyncToWorkingCopy(upstreamPath, workingCopyPath)
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
