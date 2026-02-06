package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var logsCmd = &cobra.Command{
	Use:   "logs",
	Short: "View agent logs",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("metamorph logs: not yet implemented")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(logsCmd)
}
