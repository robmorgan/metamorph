<h1>
<p align="center">
  <img src="https://github.com/user-attachments/assets/43c90f2f-1df5-46e8-adda-f485ddb8f786" alt="MetaMorph Logo" width="128">
  <br>MetaMorph
</h1>
  <p align="center">
    <strong>Orchestrate parallel Claude Code agents that coordinate through git.</strong>
  </p>
</p>

MetaMorph is a server-first CLI that launches multiple headless Claude Code agents as Docker containers. Agents claim tasks via file locks, synchronize through git push conflicts, and restart automatically on crash. No orchestration agent — each Claude instance autonomously decides what to work on next.

Based on [Anthropic's approach to building a C compiler](https://www.anthropic.com/engineering/building-c-compiler) where 16 agents built a 100,000-line compiler over 2 weeks.

## The Problem

Large coding tasks — building a compiler, rewriting a test suite, migrating a codebase — take a single AI agent days of sequential work. Context windows fill up, mistakes compound, and the human has to babysit every session. You end up with one very expensive, very slow loop.

The alternative is parallelism: split the work across multiple agents that each own a piece of the problem. But coordinating AI agents is hard. You need task assignment, conflict resolution, crash recovery, and progress tracking — without building a complex orchestration layer that itself becomes a maintenance burden.

## How It Works

```
                          ┌──────────────────────────────┐
                          │        metamorph CLI          │
                          │   init / start / stop / status│
                          └──────────┬───────────────────┘
                                     │
                          ┌──────────▼───────────────────┐
                          │     Background Daemon         │
                          │  (monitor loop every 30s)     │
                          │  - restart crashed agents     │
                          │  - clear stale task locks     │
                          │  - count commits & notify     │
                          │  - check logs for errors      │
                          └──────────┬───────────────────┘
                                     │
              ┌──────────────────────┼──────────────────────┐
              │                      │                      │
    ┌─────────▼──────┐   ┌──────────▼─────┐   ┌────────────▼────┐
    │  Docker Agent 1 │   │  Docker Agent 2 │   │  Docker Agent N  │
    │  role: developer│   │  role: tester   │   │  role: refactorer│
    └────────┬────────┘   └────────┬────────┘   └────────┬─────────┘
             │                     │                     │
             └─────────────────────┼─────────────────────┘
                                   │
                        ┌──────────▼──────────┐
                        │  Bare Git Repo      │
                        │  (.metamorph/       │
                        │   upstream.git)     │
                        │                     │
                        │  current_tasks/*.lock│
                        │  PROGRESS.md        │
                        │  AGENT_PROMPT.md    │
                        └─────────────────────┘
```

Each agent runs in a loop:

1. `git pull --rebase` to get the latest code
2. Read `PROGRESS.md` and `current_tasks/` to find unclaimed work
3. Create a `.lock` file and `git push` to claim a task (push conflict = someone else got it)
4. Run Claude Code with the agent prompt
5. Commit, push, remove the lock, repeat

## Quick Start

```bash
# 1. Install
go install github.com/brightfame/metamorph@latest

# 2. Initialize a project
cd your-project
metamorph init

# 3. Edit the config and agent prompt
vim metamorph.toml
vim AGENT_PROMPT.md

# 4. Set your API key
export ANTHROPIC_API_KEY=sk-ant-...

# 5. Start agents
metamorph start
```

Requires Docker to be running. The CLI builds a container image on first start.

## Configuration Reference

`metamorph init` generates a `metamorph.toml` in your project root:

```toml
[project]
name = "my-project"
description = ""

[agents]
count = 4                                                  # number of parallel agents
model = "claude-sonnet-4-20250514"                         # any Claude model ID
roles = ["developer", "developer", "tester", "refactorer"] # assigned round-robin

[docker]
image = "metamorph-agent:latest"                           # container image tag
extra_packages = []                                        # apt packages to install

[testing]
command = ""                                               # full test suite command
fast_command = ""                                          # quick smoke test

[notifications]
webhook_url = ""                                           # POST JSON events here
```

### CLI Commands

| Command | Description |
|---------|-------------|
| `metamorph init [dir]` | Initialize a new project (creates `metamorph.toml`, `AGENT_PROMPT.md`, `PROGRESS.md`) |
| `metamorph start` | Build the Docker image, start the daemon and all agents |
| `metamorph start -n 8` | Override agent count for this run |
| `metamorph start --model claude-sonnet-4-20250514` | Override model for this run |
| `metamorph start --dry-run` | Show what would happen without starting |
| `metamorph stop` | Stop the daemon and all agent containers, sync results |
| `metamorph status` | Show agent table with roles, tasks, and activity |
| `metamorph status --json` | Machine-readable status output |
| `metamorph logs <agent-id>` | View latest session log for an agent |
| `metamorph logs <agent-id> -f` | Follow log output in real time |
| `metamorph logs <agent-id> --tail 100` | Show last N lines (default: 50) |
| `metamorph notify --test` | Send a test webhook notification |

## Agent Roles

Each agent is assigned a role that shapes its behavior through the prompt. Roles are validated against a built-in set:

| Role | Description |
|------|-------------|
| `developer` | Implements new features and writes production code |
| `tester` | Writes and maintains test suites for code quality |
| `refactorer` | Improves code structure without changing behavior |
| `documenter` | Writes documentation, comments, and READMEs |
| `optimizer` | Profiles and optimizes performance bottlenecks |
| `reviewer` | Reviews code changes and suggests improvements |

Roles are assigned round-robin from the `roles` array. With `count = 4` and `roles = ["developer", "developer", "tester", "refactorer"]`, you get 2 developers, 1 tester, and 1 refactorer.

**Tips for role allocation:**
- Start with more `developer` agents and fewer specialized roles
- Add a `tester` early — it catches bugs from developers before they compound
- `refactorer` works best once there's enough code to clean up
- `reviewer` is useful for larger teams (6+ agents) where code review bottlenecks appear
- You can use the same role for all agents if you prefer homogeneous workers

## Customizing the Agent Prompt

`AGENT_PROMPT.md` is the system prompt every agent receives. It's processed through `envsubst` before being passed to Claude, so you can use these template variables:

| Variable | Value | Example |
|----------|-------|---------|
| `${AGENT_ID}` | Numeric agent identifier | `1`, `2`, `3` |
| `${AGENT_ROLE}` | Role from config | `developer`, `tester` |
| `${AGENT_MODEL}` | Model ID from config | `claude-sonnet-4-20250514` |

The default prompt includes:
- Identity section (who the agent is)
- Task claiming protocol (lock file workflow)
- Working conventions (commit often, run tests, prefix errors with `ERROR:`)
- Role-specific instructions for all 6 roles
- Context management tips (write to files, not stdout)

**Tips for customization:**
- Add project-specific build/test commands so agents know how to validate their work
- Include architecture notes so agents understand the codebase structure
- List known gotchas or constraints (e.g., "never modify the migration files directly")
- Keep the task claiming protocol intact — it's how agents avoid stepping on each other

## Architecture

### Daemon Process

`metamorph start` launches a background daemon that:
1. Builds the Docker image from embedded `Dockerfile` and `entrypoint.sh`
2. Starts N agent containers, each with a unique ID and role
3. Writes state to `.metamorph/state.json`
4. Runs a **monitor loop every 30 seconds** that:
   - Checks container health and restarts crashed agents
   - Reads `current_tasks/*.lock` to map tasks to agents
   - Counts new commits and batches notifications (60s window)
   - Clears stale task locks older than **2 hours**
   - Scans the last 50 lines of each agent's log for `ERROR:` or `FAIL`
   - Writes a heartbeat to `.metamorph/heartbeat`

The daemon detaches from the terminal (via `setsid`) and writes its PID to `.metamorph/daemon.pid`. `metamorph stop` sends SIGTERM and waits up to 30 seconds before SIGKILL.

### State & File Layout

```
your-project/
├── metamorph.toml            # project configuration
├── AGENT_PROMPT.md           # agent system prompt (template)
├── PROGRESS.md               # shared progress tracker
├── current_tasks/            # task lock directory
│   └── implement-parser.lock # "agent-1 2025-01-15T10:30:00Z"
├── agent_logs/               # host-mounted log directory
│   ├── agent-1/
│   │   ├── session-1.log
│   │   └── session-2.log
│   └── agent-2/
│       └── session-1.log
└── .metamorph/               # internal state (gitignored)
    ├── upstream.git/         # bare git repo (source of truth)
    ├── state.json            # daemon state (agents, stats)
    ├── daemon.pid            # daemon process ID
    ├── heartbeat             # last monitor tick (RFC3339)
    └── docker/               # build context (Dockerfile, entrypoint.sh)
```

### Docker Container

Each agent runs in a container built from `ubuntu:24.04` with:
- Node.js 22 (required by Claude Code CLI)
- `@anthropic-ai/claude-code` installed globally via npm
- `git`, `curl`, `jq`, `gettext-base`, `build-essential`

The container mounts:
- `.metamorph/upstream.git` → `/upstream` (read-only) — the bare repo agents clone from
- `agent_logs/agent-N/` → `/workspace/logs` — session logs written to the host

### Git-Based Coordination

There is no central task queue or message broker. Agents coordinate entirely through git:

- **Task claiming**: Write a `.lock` file, commit, push. If the push is rejected (another agent pushed first), reset and pick a different task.
- **Conflict resolution**: `git pull --rebase` before every session. Push conflicts are the signal that another agent claimed the work.
- **Progress tracking**: `PROGRESS.md` is a shared document that agents read and update to understand what's done, in progress, or blocked.

Lock file format: `agent-{id} {RFC3339-timestamp}` (e.g., `agent-1 2025-01-15T10:30:00Z`)

### Monitor Loop

The daemon's monitor loop runs every 30 seconds and handles:

| Check | Action |
|-------|--------|
| Container not running | Restart it, send `agent_crashed` webhook |
| Lock file older than 2h | Delete it, send `stale_lock` webhook |
| New commits detected | Batch for 60s, then send `commits_pushed` webhook |
| `ERROR:` or `FAIL` in agent log | Send `test_failure` webhook (debounced per agent, 5min cooldown) |

## Notifications

Configure a webhook URL in `metamorph.toml` to receive JSON event notifications:

```toml
[notifications]
webhook_url = "https://hooks.slack.com/services/T.../B.../xxx"
```

### Event Types

| Event | Trigger | Key Fields |
|-------|---------|------------|
| `agent_crashed` | Agent container stopped unexpectedly and was restarted | `agent_id`, `agent_role` |
| `commits_pushed` | New commits detected (batched over 60s window) | `details.count`, `details.commits` |
| `stale_lock` | Task lock older than 2 hours was cleared | `details.task` |
| `test_failure` | `ERROR:` or `FAIL` found in agent log (5min debounce per agent) | `agent_id`, `details.line` |

### Payload Format

```json
{
  "event": "agent_crashed",
  "agent_id": 2,
  "agent_role": "tester",
  "project": "my-project",
  "message": "agent-2 (tester) crashed and was restarted",
  "timestamp": "2025-01-15T10:30:00Z",
  "details": {}
}
```

### Slack Integration

For Slack, use an [Incoming Webhook](https://api.slack.com/messaging/webhooks). The payload is plain JSON — you'll need a small proxy or Slack workflow to format it, or use a service like Zapier/n8n to transform the events into Slack message blocks.

Test your webhook with:

```bash
metamorph notify --test
```

## Comparison

| | MetaMorph | Claude Code Agent Teams | claude-flow | Manual loop (`while true; do claude -p ...`) |
|-|-----------|------------------------|-------------|----------------------------------------------|
| **Coordination** | Git push conflicts | Orchestrator agent | Central coordinator | None |
| **Task assignment** | Autonomous (agents decide) | Orchestrator assigns | Central planner | Human assigns |
| **Crash recovery** | Auto-restart + webhook | Manual | Configurable | Manual |
| **Isolation** | Docker containers | Shared filesystem | Processes | Depends |
| **State** | Git repo + lock files | In-memory | Database/files | None |
| **Monitoring** | Daemon + webhooks | Built-in UI | CLI dashboard | None |
| **Scaling** | Change `count` in config | Limited by context | Configurable | Add more terminals |
| **Setup** | `metamorph init && start` | API-based | Config files | Script it yourself |

**When to use MetaMorph**: Long-running tasks (hours to days) where agents need to work autonomously, recover from crashes, and coordinate without human intervention. Best for projects where the work can be split into independent tasks.

**When not to use MetaMorph**: Quick one-off tasks, tasks requiring tight real-time coordination between agents, or when you don't want Docker as a dependency.

## Cost Estimation

Each agent runs Claude Code in a continuous loop. Costs depend on the model, context size, and how fast agents work.

**Rough formula:**

```
cost = agents x sessions_per_hour x avg_tokens_per_session x price_per_token
```

**Example estimates** (using Claude Sonnet at ~$3/1M input, $15/1M output tokens):

| Setup | Agents | Runtime | Estimated Cost |
|-------|--------|---------|---------------|
| Small task | 2 | 1 hour | $5–15 |
| Medium project | 4 | 4 hours | $40–120 |
| Large rewrite | 8 | 8 hours | $150–500 |
| Compiler-scale (Anthropic blog) | 16 | 2 weeks | $thousands |

These are rough estimates. Actual costs vary based on task complexity, how often agents hit context limits and restart sessions, and the model used. Use `metamorph status` to track session counts and commits as a proxy for activity.

**Tips to manage costs:**
- Start with fewer agents and scale up once you've validated the approach
- Use `--dry-run` to verify configuration before starting
- Monitor with `metamorph status` and stop early if agents are thrashing
- Use Sonnet for most work; reserve Opus for complex reasoning tasks

## Worked Example: JSON Parser in Rust

Here's how you might use MetaMorph to build a JSON parser with 4 agents:

**1. Initialize:**
```bash
mkdir json-parser && cd json-parser
cargo init
metamorph init
```

**2. Configure `metamorph.toml`:**
```toml
[project]
name = "json-parser"
description = "A spec-compliant JSON parser in Rust"

[agents]
count = 4
model = "claude-sonnet-4-20250514"
roles = ["developer", "developer", "tester", "refactorer"]

[testing]
command = "cargo test"
fast_command = "cargo test --lib"
```

**3. Customize `AGENT_PROMPT.md`** — add project-specific context:
```markdown
## Project Goal
Build a JSON parser in Rust that passes the JSONTestSuite.
The parser should handle: null, booleans, numbers, strings (with escapes),
arrays, and objects.

## Architecture
- `src/lexer.rs` — tokenizer
- `src/parser.rs` — recursive descent parser
- `src/value.rs` — JSON value types
- `src/error.rs` — error types
- `src/lib.rs` — public API

## Task List
1. Implement the lexer (tokenize JSON input)
2. Implement JSON value types
3. Implement the recursive descent parser
4. Add string escape handling (\n, \t, \uXXXX)
5. Add number parsing (integers, floats, exponents)
6. Write property-based tests with proptest
7. Download and run JSONTestSuite
8. Optimize: avoid unnecessary allocations
9. Add serde compatibility
10. Write README with usage examples
```

**4. Start:**
```bash
export ANTHROPIC_API_KEY=sk-ant-...
metamorph start
```

**5. Monitor progress:**
```bash
metamorph status
metamorph logs 1 -f   # watch agent-1 work
```

What happens: Agent 1 (developer) claims "Implement the lexer", agent 2 (developer) claims "Implement JSON value types", agent 3 (tester) starts writing tests against the API, agent 4 (refactorer) waits for code to review then starts improving structure. When agent 1 finishes the lexer, it moves on to the parser. When agent 3 finds a bug, it files it in `PROGRESS.md` and agent 1 picks it up next session.

## FAQ

**Do agents actually coordinate, or do they step on each other?**
They coordinate through git. The lock file + push mechanism means only one agent can claim a given task. Agents read `PROGRESS.md` and `git log` to understand what others are doing. Conflicts happen occasionally but `git pull --rebase` resolves most of them.

**What happens when an agent crashes?**
The daemon detects the stopped container within 30 seconds, restarts it, and sends an `agent_crashed` webhook. The agent starts a new session, pulls the latest code, and picks up where it left off (or claims a new task).

**Can I use Opus instead of Sonnet?**
Yes. Set `model = "claude-opus-4-20250514"` in `metamorph.toml`. Opus is better at complex reasoning but significantly more expensive. A common pattern is to run most agents on Sonnet with one Opus agent for the hardest tasks.

**How do I add project dependencies (Python, Rust, etc.)?**
Add system packages to `extra_packages` in `metamorph.toml`. For language-specific toolchains, you may need to customize the Dockerfile. The embedded Dockerfile is written to `.metamorph/docker/Dockerfile` on first build — you can edit it there.

**Can I run this without Docker?**
Not currently. Docker provides isolation between agents (separate filesystems, no interference) and makes crash recovery simple (just restart the container). Running agents as bare processes would require a different coordination mechanism.

**How do I see what agents are doing right now?**
`metamorph status` shows each agent's role, status, current task, and last activity. `metamorph logs <id> -f` follows an agent's session output in real time.

**What happens when I run `metamorph stop`?**
The daemon stops all containers, syncs the upstream bare repo to a working copy at `.metamorph/work`, and prints a session summary (commits, sessions, tasks completed). Your project directory contains the final state of all agent work.

## Credits

MetaMorph is inspired by [Anthropic's engineering blog post on building a C compiler with Claude](https://www.anthropic.com/engineering/building-c-compiler), which demonstrated that multiple AI agents working in parallel via git can tackle large-scale software projects effectively.

Built with [Claude Code](https://docs.anthropic.com/en/docs/claude-code) by Anthropic.

## License

Apache 2.0 — see [LICENSE](LICENSE) for details.
