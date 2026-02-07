package assets

import _ "embed"

//go:embed system_prompt.md
var SystemPrompt string

//go:embed entrypoint.sh
var DefaultEntrypoint string

//go:embed Dockerfile
var DefaultDockerfile string
