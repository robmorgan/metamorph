package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/robmorgan/metamorph/internal/controlplane"
	"github.com/robmorgan/metamorph/internal/config"
	"github.com/robmorgan/metamorph/internal/constants"
	"github.com/robmorgan/metamorph/internal/gitops"
	"github.com/spf13/cobra"
)

var reposCmd = &cobra.Command{
	Use:   "repos",
	Short: "Manage project repositories",
}

var reposAddCmd = &cobra.Command{
	Use:   "add <path>",
	Short: "Add a repository to the current project",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		projectName, cfg, err := resolveProject(cmd)
		if err != nil {
			return err
		}

		absPath, err := filepath.Abs(args[0])
		if err != nil {
			return fmt.Errorf("failed to resolve path: %w", err)
		}

		// Verify it's a git repo.
		if _, err := os.Stat(filepath.Join(absPath, ".git")); os.IsNotExist(err) {
			return fmt.Errorf("directory is not a git repository: %s", absPath)
		}

		label, _ := cmd.Flags().GetString("label")
		if label == "" {
			label = filepath.Base(absPath)
		}

		// Check for duplicate label.
		for _, r := range cfg.Repos {
			if config.RepoLabel(r) == label {
				return fmt.Errorf("repo with label %q already exists", label)
			}
		}

		// Add to config.
		cfg.Repos = append(cfg.Repos, config.RepoConfig{Path: absPath, Label: label})
		configPath := filepath.Join(constants.ProjectDir(projectName), constants.ProjectConfigFile)
		if err := config.Save(cfg, configPath); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		// Register in broker DB.
		dbPath := constants.ControlPlaneDBPath()
		store, err := controlplane.NewSQLiteStore(dbPath)
		if err != nil {
			return fmt.Errorf("failed to open database: %w", err)
		}
		defer store.Close()

		ctx := context.Background()
		if err := store.AddProjectRepo(ctx, projectName, absPath, label); err != nil {
			return fmt.Errorf("failed to register repo: %w", err)
		}

		// If upstream repos dir exists (agents have been started before), create bare clone.
		reposDir := filepath.Join(constants.ProjectDir(projectName), constants.ReposDir)
		if _, err := os.Stat(reposDir); err == nil {
			upstreamPath := constants.UpstreamRepoPath(projectName, label)
			if _, err := os.Stat(upstreamPath); os.IsNotExist(err) {
				fmt.Printf("Creating upstream bare clone for %q...\n", label)
				if err := gitops.InitUpstream(absPath, upstreamPath); err != nil {
					return fmt.Errorf("failed to init upstream for %q: %w", label, err)
				}
			}
		}

		fmt.Printf("Added repo %q (%s) to project %q\n", label, absPath, projectName)
		return nil
	},
}

var reposListCmd = &cobra.Command{
	Use:   "list",
	Short: "List repositories for the current project",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectName, cfg, err := resolveProject(cmd)
		if err != nil {
			return err
		}

		if len(cfg.Repos) == 0 {
			fmt.Printf("No repos configured for project %q\n", projectName)
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "LABEL\tPATH")
		for _, r := range cfg.Repos {
			_, _ = fmt.Fprintf(w, "%s\t%s\n", config.RepoLabel(r), r.Path)
		}
		_ = w.Flush()
		return nil
	},
}

var reposRemoveCmd = &cobra.Command{
	Use:   "remove <label>",
	Short: "Remove a repository from the current project",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		projectName, cfg, err := resolveProject(cmd)
		if err != nil {
			return err
		}

		label := args[0]
		found := false
		var newRepos []config.RepoConfig
		for _, r := range cfg.Repos {
			if config.RepoLabel(r) == label {
				found = true
				continue
			}
			newRepos = append(newRepos, r)
		}

		if !found {
			return fmt.Errorf("no repo with label %q found", label)
		}

		if len(newRepos) == 0 {
			return fmt.Errorf("cannot remove the last repo from a project")
		}

		cfg.Repos = newRepos
		configPath := filepath.Join(constants.ProjectDir(projectName), constants.ProjectConfigFile)
		if err := config.Save(cfg, configPath); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		fmt.Printf("Removed repo %q from project %q\n", label, projectName)
		return nil
	},
}

func init() {
	reposAddCmd.Flags().String("label", "", "Label for the repo (defaults to directory basename)")
	reposCmd.AddCommand(reposAddCmd)
	reposCmd.AddCommand(reposListCmd)
	reposCmd.AddCommand(reposRemoveCmd)
	rootCmd.AddCommand(reposCmd)
}
