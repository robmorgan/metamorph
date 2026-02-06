package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/robmorgan/metamorph/internal/constants"
	"github.com/spf13/cobra"
)

var promptCmd = &cobra.Command{
	Use:   "prompt",
	Short: "Manage agent prompt templates",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectDir, err := resolveProjectDir()
		if err != nil {
			return err
		}

		promptPath := filepath.Join(projectDir, constants.AgentPromptFile)

		editFlag, _ := cmd.Flags().GetBool("edit")

		if editFlag {
			editor := os.Getenv("EDITOR")
			if editor == "" {
				editor = "vi"
			}
			c := exec.Command(editor, promptPath)
			c.Stdin = os.Stdin
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			return c.Run()
		}

		// Default: --show behavior.
		data, err := os.ReadFile(promptPath)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("AGENT_PROMPT.md not found (run 'metamorph init' first)")
			}
			return fmt.Errorf("failed to read AGENT_PROMPT.md: %w", err)
		}

		fmt.Print(string(data))
		return nil
	},
}

func init() {
	promptCmd.Flags().Bool("show", false, "Show the agent prompt (default)")
	promptCmd.Flags().Bool("edit", false, "Open the agent prompt in $EDITOR")
	rootCmd.AddCommand(promptCmd)
}
