package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Sync changes between upstream and agent worktrees",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("metamorph sync: not yet implemented")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(syncCmd)
}
