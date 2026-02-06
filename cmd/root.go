package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "metamorph",
	Short: "Metamorph - AI-powered parallel development agents",
	Long:  "Metamorph orchestrates multiple AI coding agents working in parallel on your codebase.",
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
