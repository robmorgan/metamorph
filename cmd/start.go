package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the metamorph daemon and agents",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("metamorph start: not yet implemented")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(startCmd)
}
