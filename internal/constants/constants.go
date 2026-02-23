package constants

import (
	"os"
	"path/filepath"
)

// Global daemon state paths (under ~/.metamorph/).
const (
	GlobalDirName      = ".metamorph"
	GlobalConfigFile   = "config.json"
	DaemonPIDFile      = "daemon.pid"
	DaemonLogFile      = "daemon.log"
	ControlPlaneDBFile = "broker.db" // kept as broker.db for backwards compat
	DockerDir          = "docker"
	ProjectsDir        = "projects"
)

// Per-project paths (under ~/.metamorph/projects/{name}/).
const (
	ProjectConfigFile = "config.json"
	ProjectStateFile  = "state.json"
	AgentLogDir       = "agent_logs"
	ReposDir          = "repos"
)

// Files that live in the project source repo (not under ~/.metamorph/).
const (
	TaskLockDir      = "current_tasks"
	ProgressFile     = "PROGRESS.md"
	AgentPromptFile  = "AGENT_PROMPT.md"
	SystemPromptFile = "SYSTEM_PROMPT.md"
)

// DefaultControlPlanePort is the default port for the control plane HTTP API.
const DefaultControlPlanePort = 8377

// AgentRoles maps built-in role names to their descriptions.
var AgentRoles = map[string]string{
	"developer":  "Implements new features and writes production code",
	"tester":     "Writes and maintains test suites for code quality",
	"refactorer": "Improves code structure without changing behavior",
	"documenter": "Writes documentation, comments, and READMEs",
	"optimizer":  "Profiles and optimizes performance bottlenecks",
	"reviewer":   "Reviews code changes and suggests improvements",
}

// GlobalDir returns the path to the global metamorph directory (~/.metamorph/).
func GlobalDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", GlobalDirName)
	}
	return filepath.Join(home, GlobalDirName)
}

// ProjectDir returns the path to a project's state directory under the global dir.
func ProjectDir(projectName string) string {
	return filepath.Join(GlobalDir(), ProjectsDir, projectName)
}

// GlobalConfigPath returns the path to the global config file.
func GlobalConfigPath() string {
	return filepath.Join(GlobalDir(), GlobalConfigFile)
}

// ControlPlaneDBPath returns the path to the control plane SQLite database.
func ControlPlaneDBPath() string {
	return filepath.Join(GlobalDir(), ControlPlaneDBFile)
}

// UpstreamRepoPath returns the path to a project's upstream bare repo for a given label.
func UpstreamRepoPath(projectName, repoLabel string) string {
	return filepath.Join(ProjectDir(projectName), ReposDir, repoLabel+".git")
}

// WorkDir returns the path to a project's working copy directory.
func WorkDir(projectName string) string {
	return filepath.Join(ProjectDir(projectName), "work")
}

// ProjectAgentLogDir returns the path to a project's agent logs directory.
func ProjectAgentLogDir(projectName string) string {
	return filepath.Join(ProjectDir(projectName), AgentLogDir)
}
