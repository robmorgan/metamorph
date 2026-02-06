package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the status of running agents",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("metamorph status: not yet implemented")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
