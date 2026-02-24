package cmd

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/robmorgan/metamorph/internal/constants"
	"github.com/robmorgan/metamorph/internal/logparse"
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

		projectName, err := resolveProjectName(cmd)
		if err != nil {
			return err
		}

		follow, _ := cmd.Flags().GetBool("follow")
		tail, _ := cmd.Flags().GetInt("tail")

		logDir := filepath.Join(constants.ProjectAgentLogDir(projectName), fmt.Sprintf("agent-%d", agentID))

		// Find the latest session log file.
		logFile, err := logparse.FindLatestLog(logDir)
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
			if formatted, ok := logparse.FormatLogLine(line); ok {
				fmt.Println(formatted)
			}
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
					_ = f.Close()
					continue
				}

				if info.Size() <= offset {
					_ = f.Close()
					continue
				}

				if _, err := f.Seek(offset, io.SeekStart); err != nil {
					_ = f.Close()
					continue
				}

				newData, err := io.ReadAll(f)
				_ = f.Close()
				if err != nil {
					continue
				}

				if len(newData) > 0 {
					offset += int64(len(newData))
					newLines := strings.Split(string(newData), "\n")
					for _, line := range newLines {
						if formatted, ok := logparse.FormatLogLine(line); ok {
							fmt.Println(formatted)
						}
					}
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
