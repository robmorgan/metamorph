package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run a one-shot agent task",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("metamorph run: not yet implemented")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(runCmd)
}
