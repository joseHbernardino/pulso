<div align="center">

# Pulso

<img src="assets/brand/pulso-logo.svg" width="264" alt="Pulso" />

A modern process manager and application supervisor written in Go.

Pulso manages your applications — Go, Python, Node.js/TypeScript, Bun, Deno, Ruby, and PHP — with a unified CLI, TUI, Web UI, and MCP interface. Keep your applications in rhythm, with one workspace for process control.

</div>

[Visual identity and assets](docs/identity.md) · [Publishing](docs/publishing.md)

## Features

- **Multi-runtime support** — Go, Python, Node.js/TypeScript, Bun, Deno, Ruby, PHP
- **Auto-detection** — Pulso detects your project's runtime automatically
- **Process lifecycle** — start, stop, restart, reload, with configurable restart policies
- **Exponential backoff** — crash-loop protection with configurable max restarts
- **Log management** — per-process log capture, rotation, and streaming
- **Persistence** — save and resurrect process state across restarts
- **Watch mode** — auto-restart on file changes with configurable debounce
- **Cron scheduling** — scheduled restarts and task execution
- **TUI dashboard** — terminal UI with process table, log viewer, and keybindings
- **Web UI** — browser-based dashboard with WebSocket live updates
- **MCP support** — AI agent integration via Model Context Protocol
- **Daemon mode** — background daemon with HTTP-over-Unix-socket IPC
- **Self-update** — built-in binary update from GitHub releases
- **System service** — systemd and launchd integration for boot-time startup
- **Lifecycle hooks** — run commands before/after start, stop, restart, and reload
- **Resource metrics** — per-process CPU, memory, thread, and FD tracking

## Install

Build from this source tree with Go 1.26.11 or newer:

```bash
make build
./bin/pulso version
```

To install for your user:

```bash
mkdir -p "$HOME/.local/bin"
cp bin/pulso "$HOME/.local/bin/pulso"
```

Ensure `$HOME/.local/bin` is on your `PATH`.

Downloads and `pulso update` require your own GitHub release repository. Set
`PULSO_RELEASE_REPOSITORY=owner/repository` to the repository you publish to.
No public download destination is configured in development builds. The local
module name `pulso.local/pulso` supports builds from this checkout; it is not a
published Go package address.

For an already published release:

```bash
PULSO_RELEASE_REPOSITORY=owner/repository bash scripts/install.sh v1.0.0
```

## Quick Start

```bash
# Start a process (auto-detects runtime)
pulso start app.py
pulso start server.go
pulso start index.ts
pulso start dev --runtime bun

# List running processes
pulso list

# View logs
pulso logs app.py
pulso logs app.py -f       # follow mode

# Stop, restart, reload
pulso stop app.py
pulso restart app.py
pulso reload app.py

# Save and restore
pulso save
pulso resurrect

# Launch TUI dashboard
pulso tui

# Launch Web dashboard
pulso web

# Check environment
pulso doctor
```

## Daemon

You can also create applications in the browser: open `pulso web`, click
**Nova aplicação**, and enter a program or script already on the server. The
form supports runtime selection, working directories, arguments, environment
variables and restart policies. The daemon dashboard requires login and stores
accounts and sessions in SQLite. On first access, create an administrator using
the server setup code; see [login and permissions](docs/access-control.md). See the [web dashboard guide](docs/web-dashboard.md). For first-time administrator setup and admin/view roles, see [access control](docs/access-control.md).

Pulso can run as a background daemon for persistent process management:

```bash
# Start daemon
pulso daemon start

# Check daemon status
pulso daemon status

# Stop daemon
pulso daemon stop
```

The CLI automatically starts the daemon when needed. All commands work with or without a running daemon — without it, the CLI operates in direct mode.

## Configuration

Create a `pulso.yaml` in your project root:

```yaml
processes:
  - name: "api"
    runtime: "go"
    entrypoint: "./cmd/api"
    args: ["--port", "8080"]
    env:
      PORT: "8080"
    restart_policy: "always"
    max_restarts: 5

  - name: "worker"
    runtime: "python"
    entrypoint: "worker.py"
    restart_policy: "on-failure"

  - name: "frontend"
    runtime: "bun"
    entrypoint: "dev"
    watch:
      enabled: true
      paths: ["./src"]
```

See `configs/pulso.example.yaml` for the full configuration reference.

### Lifecycle Hooks

Run commands at defined points in the process lifecycle:

```yaml
processes:
  - name: "api"
    entrypoint: "./api"
    hooks:
      pre_start:
        command: "echo 'Starting API'"
        timeout: "5s"
      post_start:
        command: "curl -sf http://localhost:8080/health || true"
        ignore_failure: true
      pre_stop:
        command: "echo 'Draining connections'"
      post_stop:
        command: "echo 'Stopped'"
      pre_restart:
        command: "echo 'Restarting'"
      post_restart:
        command: "curl -sf http://localhost:8080/health"
```

**Behavior:**

- `pre_*` hooks can **block** the lifecycle action on failure (unless `ignore_failure: true`)
- `post_*` hooks are **informational** — failures are logged but don't undo the action
- Each hook has a configurable timeout (default: 30s)
- Hook output is captured in structured logs

## CLI Commands

| Command                         | Description                            |
| ------------------------------- | -------------------------------------- |
| `pulso start <entrypoint>`      | Start a managed process                |
| `pulso stop <id\|name\|all>`    | Stop process(es)                       |
| `pulso restart <id\|name\|all>` | Restart process(es)                    |
| `pulso reload <id\|name>`       | Graceful reload                        |
| `pulso delete <id\|name\|all>`  | Stop and remove from table             |
| `pulso list`                    | Show process table                     |
| `pulso status <id\|name>`       | Detailed process info                  |
| `pulso inspect <id\|name>`      | Full process details + logs + TUI      |
| `pulso logs <id\|name>`         | View/stream logs                       |
| `pulso save`                    | Persist process state                  |
| `pulso resurrect`               | Restore saved processes                |
| `pulso startup`                 | Install boot service (systemd/launchd) |
| `pulso update`                  | Update Pulso binary                    |
| `pulso version`                 | Print version                          |
| `pulso tui`                     | Launch terminal UI                     |
| `pulso web`                     | Launch web dashboard                   |
| `pulso cron`                    | Manage cron jobs                       |
| `pulso watch`                   | Enable file watching                   |
| `pulso doctor`                  | Run diagnostics                        |
| `pulso daemon`                  | Manage background daemon               |
| `pulso mcp`                     | Start MCP server for AI agents         |
| `pulso validate`                | Validate a configuration file          |
| `pulso flush`                   | Flush process logs                     |
| `pulso migrate`                 | Migrate data between versions          |
| `pulso events`                  | Stream process events                  |
| `pulso ready`                   | Check if process is ready              |
| `pulso config reload`           | Reload daemon configuration            |

## MCP Integration

Pulso provides an MCP server for AI agent integration. Configure it in your AI tool's settings:

```json
{
  "mcpServers": {
    "pulso": {
      "command": "pulso",
      "args": ["mcp"]
    }
  }
}
```

Available MCP tools: `list_apps`, `start_app`, `stop_app`, `restart_app`, `reload_app`, `delete_app`, `get_status`, `get_logs`, `save_state`, `resurrect_processes`.

MCP resources: `apps://list`, `apps://{name}`, `logs://{name}`, `metrics://{name}`.

## Architecture

```
 ┌─────────┐  ┌─────────┐  ┌─────────┐  ┌─────────┐
 │   CLI   │  │   TUI   │  │  Web UI │  │   MCP   │
 └────┬────┘  └────┬────┘  └────┬────┘  └────┬────┘
      │            │            │            │
      └────────────┴────────────┴────────────┘
                         │
                ┌────────┴────────┐
                │   IPC / Direct  │
                └────────┬────────┘
                         │
                ┌────────┴────────┐
                │   Supervisor    │
                │   Engine        │
                └────────┬────────┘
                         │
     ┌─────────┬─────────┼─────────┬─────────┐
     │         │         │         │         │
 ┌───┴───┐ ┌───┴───┐ ┌───┴───┐ ┌───┴───┐ ┌──┴──┐
 │  Go   │ │Python │ │ Node  │ │  Bun  │ │ ... │
 └───────┘ └───────┘ └───────┘ └───────┘ └─────┘
```

## Documentation

Full documentation is available in the [`docs/`](docs/) directory:

- [CLI Reference](docs/cli-reference.md) — all commands, flags, and examples
- [Configuration](docs/configuration.md) — YAML schema, fields, and validation
- [Architecture](docs/architecture.md) — system design and module breakdown
- [Hooks](docs/hooks.md) — lifecycle hooks and execution behavior
- [Reload, Watch, and Cron](docs/reload-watch-cron.md) — automated process management
- [Logging](docs/logging.md) — log storage, rotation, and reading
- [MCP Integration](docs/mcp.md) — AI agent tools and resources
- [Troubleshooting](docs/troubleshooting.md) — common issues and debugging
- [Developer Guide](docs/developer-guide.md) — contributing and extending Pulso

## Development

```bash
make build    # Build binary
make test     # Run tests
make vet      # Run go vet
make lint     # Run linter (requires golangci-lint)
make clean    # Remove build artifacts
```

## License

MIT
