package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var tasksCmd = &cobra.Command{
	Use:   "tasks",
	Short: "Manage and view agent tasks",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("metamorph tasks: not yet implemented")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(tasksCmd)
}
