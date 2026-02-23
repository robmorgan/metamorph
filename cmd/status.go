package cmd

import (
	"fmt"

	"github.com/robmorgan/metamorph/internal/daemon"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show daemon status",
	RunE: func(cmd *cobra.Command, args []string) error {
		if !daemon.IsRunning() {
			fmt.Println("Daemon is not running.")
			return nil
		}
		fmt.Println("Daemon is running.")
		return nil
	},
}

func init() {
	statusCmd.Flags().Bool("json", false, "Output status as JSON")
	rootCmd.AddCommand(statusCmd)
}
