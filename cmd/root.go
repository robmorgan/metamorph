package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/robmorgan/metamorph/internal/controlplane"
	"github.com/robmorgan/metamorph/internal/config"
	"github.com/robmorgan/metamorph/internal/constants"
	"github.com/spf13/cobra"
)

var verbose bool

var rootCmd = &cobra.Command{
	Use:           "metamorph",
	Short:         "Metamorph - AI-powered parallel development agents",
	Long:          "Metamorph orchestrates multiple AI coding agents working in parallel on your codebase.",
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		level := slog.LevelInfo
		if verbose {
			level = slog.LevelDebug
		}
		handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
		slog.SetDefault(slog.New(handler))
		return nil
	},
}

func init() {
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose (debug) logging")
	rootCmd.PersistentFlags().String("project", "", "Project name (overrides CWD detection)")
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}
}

// resolveProject resolves the current project by name or CWD and loads its config.
func resolveProject(cmd *cobra.Command) (string, *config.Config, error) {
	// 1. Check --project flag.
	if name, _ := cmd.Flags().GetString("project"); name != "" {
		cfg, err := config.LoadProject(name)
		if err != nil {
			return "", nil, fmt.Errorf("failed to load project %q: %w", name, err)
		}
		return name, cfg, nil
	}

	// 2. Find git repo root from CWD.
	repoRoot, err := findGitRoot()
	if err != nil {
		return "", nil, fmt.Errorf("not in a git repository (run 'git init' first)")
	}

	// 3. Query SQLite for project name from repo path.
	dbPath := constants.ControlPlaneDBPath()
	store, err := controlplane.NewSQLiteStore(dbPath)
	if err != nil {
		return "", nil, fmt.Errorf("metamorph not initialized (run 'metamorph project create' first)")
	}
	defer store.Close()

	name, err := store.GetProjectByRepoPath(context.Background(), repoRoot)
	if err != nil {
		return "", nil, fmt.Errorf("not a registered project. Run 'metamorph project create' first.")
	}

	// 4. Load config.
	cfg, err := config.LoadProject(name)
	if err != nil {
		return "", nil, fmt.Errorf("failed to load project config: %w", err)
	}

	return name, cfg, nil
}

// resolveProjectName resolves just the project name (without loading config).
func resolveProjectName(cmd *cobra.Command) (string, error) {
	if name, _ := cmd.Flags().GetString("project"); name != "" {
		return name, nil
	}

	repoRoot, err := findGitRoot()
	if err != nil {
		return "", fmt.Errorf("not in a git repository")
	}

	dbPath := constants.ControlPlaneDBPath()
	store, err := controlplane.NewSQLiteStore(dbPath)
	if err != nil {
		return "", fmt.Errorf("metamorph not initialized (run 'metamorph project create' first)")
	}
	defer store.Close()

	name, err := store.GetProjectByRepoPath(context.Background(), repoRoot)
	if err != nil {
		return "", fmt.Errorf("not a registered project. Run 'metamorph project create' first.")
	}

	return name, nil
}

// findGitRoot walks up from CWD to find the nearest .git directory.
func findGitRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("not in a git repository")
		}
		dir = parent
	}
}

// controlPlaneURL returns the HTTP URL for the control plane API.
func controlPlaneURL() string {
	cfg, err := config.LoadGlobal()
	if err != nil {
		return fmt.Sprintf("http://localhost:%d", constants.DefaultControlPlanePort)
	}
	return fmt.Sprintf("http://localhost:%d", cfg.Daemon.Port)
}

// formatDuration formats seconds into a human-readable string like "2h 15m 30s".
func formatDuration(secs int) string {
	d := time.Duration(secs) * time.Second
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := secs % 60

	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

// formatRelativeTime formats a time.Time as a relative string like "2m ago".
func formatRelativeTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
