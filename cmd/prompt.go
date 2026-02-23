package cmd

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/robmorgan/metamorph/assets"
	"github.com/robmorgan/metamorph/internal/constants"
	"github.com/spf13/cobra"
)

var promptCmd = &cobra.Command{
	Use:   "prompt",
	Short: "Manage agent prompt templates",
	RunE: func(cmd *cobra.Command, args []string) error {
		showSystem, _ := cmd.Flags().GetBool("show-system")

		if showSystem {
			fmt.Print(assets.SystemPrompt)
			return nil
		}

		_, cfg, err := resolveProject(cmd)
		if err != nil {
			return err
		}

		if len(cfg.Repos) == 0 {
			return fmt.Errorf("no repos configured")
		}
		repoPath := cfg.Repos[0].Path
		promptPath := repoPath + "/" + constants.AgentPromptFile

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
				return fmt.Errorf("AGENT_PROMPT.md not found (run 'metamorph project create' first)")
			}
			return fmt.Errorf("failed to read AGENT_PROMPT.md: %w", err)
		}

		fmt.Print(string(data))
		return nil
	},
}

func init() {
	promptCmd.Flags().Bool("show", false, "Show the agent prompt (default)")
	promptCmd.Flags().Bool("show-system", false, "Show the built-in system prompt")
	promptCmd.Flags().Bool("edit", false, "Open the agent prompt in $EDITOR")
	rootCmd.AddCommand(promptCmd)
}
