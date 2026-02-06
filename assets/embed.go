package assets

import _ "embed"

//go:embed agent_prompt.md
var DefaultAgentPrompt string

//go:embed entrypoint.sh
var DefaultEntrypoint string

//go:embed Dockerfile
var DefaultDockerfile string
