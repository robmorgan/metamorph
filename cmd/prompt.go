package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var promptCmd = &cobra.Command{
	Use:   "prompt",
	Short: "Manage agent prompt templates",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("metamorph prompt: not yet implemented")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(promptCmd)
}
