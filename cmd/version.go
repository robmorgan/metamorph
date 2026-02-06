package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	version = "dev"
	commit  = ""
	date    = ""
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version of metamorph",
	Run: func(cmd *cobra.Command, args []string) {
		if commit != "" && date != "" {
			fmt.Printf("metamorph version %s (commit %s, built %s)\n", version, commit, date)
		} else {
			fmt.Printf("metamorph version %s\n", version)
		}
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
