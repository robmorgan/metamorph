package cmd

import (
	"fmt"

	"github.com/robmorgan/metamorph/internal/daemon"
	"github.com/spf13/cobra"
)

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the metamorph daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		if !daemon.IsRunning() {
			return fmt.Errorf("daemon is not running")
		}

		fmt.Println("Stopping daemon...")
		if err := daemon.Stop(); err != nil {
			return err
		}
		fmt.Println("Daemon stopped.")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(stopCmd)
}
