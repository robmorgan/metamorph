package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the metamorph daemon and agents",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("metamorph stop: not yet implemented")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(stopCmd)
}
