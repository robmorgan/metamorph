package cmd

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/brightfame/metamorph/internal/constants"
	"github.com/spf13/cobra"
)

var logsCmd = &cobra.Command{
	Use:   "logs <agent-id>",
	Short: "View agent logs",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		agentID, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid agent ID %q: must be a number", args[0])
		}

		projectDir, err := resolveProjectDir()
		if err != nil {
			return err
		}

		follow, _ := cmd.Flags().GetBool("follow")
		tail, _ := cmd.Flags().GetInt("tail")

		logDir := filepath.Join(projectDir, constants.AgentLogDir, fmt.Sprintf("agent-%d", agentID))

		// Find the latest session log file.
		logFile, err := findLatestLog(logDir)
		if err != nil {
			return err
		}

		// Read the file.
		data, err := os.ReadFile(logFile)
		if err != nil {
			return fmt.Errorf("failed to read log file: %w", err)
		}

		// Print last N lines.
		lines := strings.Split(string(data), "\n")
		start := 0
		if tail > 0 && tail < len(lines) {
			start = len(lines) - tail
		}
		for _, line := range lines[start:] {
			fmt.Println(line)
		}

		if !follow {
			return nil
		}

		// Follow mode: poll for new content.
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

		offset := int64(len(data))
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-sigCh:
				return nil
			case <-ticker.C:
				f, err := os.Open(logFile)
				if err != nil {
					continue
				}

				info, err := f.Stat()
				if err != nil {
					f.Close()
					continue
				}

				if info.Size() <= offset {
					f.Close()
					continue
				}

				if _, err := f.Seek(offset, io.SeekStart); err != nil {
					f.Close()
					continue
				}

				newData, err := io.ReadAll(f)
				f.Close()
				if err != nil {
					continue
				}

				if len(newData) > 0 {
					fmt.Print(string(newData))
					offset += int64(len(newData))
				}
			}
		}
	},
}

func init() {
	logsCmd.Flags().BoolP("follow", "f", false, "Follow log output")
	logsCmd.Flags().Int("tail", 50, "Number of lines to show from the end")
	rootCmd.AddCommand(logsCmd)
}

// findLatestLog finds the most recent session-*.log file in the given directory.
func findLatestLog(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("no logs found for this agent (directory does not exist)")
		}
		return "", fmt.Errorf("failed to read log directory: %w", err)
	}

	var logFiles []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "session-") && strings.HasSuffix(e.Name(), ".log") {
			logFiles = append(logFiles, e.Name())
		}
	}

	if len(logFiles) == 0 {
		return "", fmt.Errorf("no session log files found in %s", dir)
	}

	// Sort by session number (session-1.log, session-2.log, ...).
	sort.Slice(logFiles, func(i, j int) bool {
		ni := extractSessionNumber(logFiles[i])
		nj := extractSessionNumber(logFiles[j])
		return ni < nj
	})

	return filepath.Join(dir, logFiles[len(logFiles)-1]), nil
}

// extractSessionNumber parses the number from "session-N.log".
func extractSessionNumber(name string) int {
	name = strings.TrimPrefix(name, "session-")
	name = strings.TrimSuffix(name, ".log")
	n, _ := strconv.Atoi(name)
	return n
}
