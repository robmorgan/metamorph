package assets

import _ "embed"

//go:embed SYSTEM_PROMPT.md
var SystemPrompt string

//go:embed entrypoint.sh
var DefaultEntrypoint string

//go:embed Dockerfile
var DefaultDockerfile string
