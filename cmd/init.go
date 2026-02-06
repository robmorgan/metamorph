package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a new metamorph project",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("metamorph init: not yet implemented")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
}
