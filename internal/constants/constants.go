package constants

// Standard paths used by metamorph.
const (
	UpstreamDir     = ".metamorph/upstream.git"
	StateFile       = ".metamorph/state.json"
	DockerDir       = ".metamorph/docker"
	TaskLockDir     = "current_tasks"
	AgentLogDir     = "agent_logs"
	ProgressFile    = "PROGRESS.md"
	AgentPromptFile = "AGENT_PROMPT.md"
	DaemonPIDFile   = ".metamorph/daemon.pid"
	HeartbeatFile   = ".metamorph/heartbeat"
)

// AgentRoles maps built-in role names to their descriptions.
var AgentRoles = map[string]string{
	"developer":   "Implements new features and writes production code",
	"tester":      "Writes and maintains test suites for code quality",
	"refactorer":  "Improves code structure without changing behavior",
	"documenter":  "Writes documentation, comments, and READMEs",
	"optimizer":   "Profiles and optimizes performance bottlenecks",
	"reviewer":    "Reviews code changes and suggests improvements",
}
