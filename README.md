<div align="center">

# Pulso

<img src="assets/brand/pulso-logo.svg" width="264" alt="Pulso" />

A modern process manager and application supervisor written in Go.

Pulso manages your applications — Go, Python, Node.js/TypeScript, Bun, Deno, Ruby, and PHP — with a unified CLI, TUI, Web UI, and MCP interface. Keep your applications in rhythm, with one workspace for process control.

</div>

[Visual identity and assets](docs/identity.md) · [Publishing](docs/publishing.md)

## Features

- **Application containers** — deploy images, Node.js, Python and Dockerfile projects from the dashboard with separate environments, persistent storage and resource limits
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
- **Container metrics** — per-application CPU, memory, network and block I/O, with page cache discounted
- **Audit log** — append-only record of who changed what, including refused attempts
- **Notifications** — outbound alerts to Slack, Discord, Telegram, Gotify, ntfy, a webhook or e-mail
- **Docker inventory** — manage every container, image, volume and network on the Engine, with the panel's own infrastructure protected
- **Disk cleanup** — truthful dry run that lists exactly what would be freed, never removing tagged images or protected resources

## Install

Pulso runs as a container and deploys each application as its own container.
One command, on any Linux distribution:

```bash
curl -fsSL https://raw.githubusercontent.com/joseHbernardino/pulso/main/scripts/get-pulso.sh | sudo sh
```

The installer detects the distribution, installs Docker Engine when missing,
handles SELinux and cgroups, and brings up the panel and its privileged agent.
It needs no repository checkout, no Go, Node.js or Python, and no Docker
Compose. Validated on Debian, Ubuntu, RHEL, Rocky, AlmaLinux, Fedora, Amazon
Linux 2 and 2023, openSUSE, Arch and Alpine — see the
[installation guide](docs/install.md) for options, closed networks and upgrades.

New installations give administrators read access to the server's `/` through
a read-only mount. The panel runs as root with `DAC_OVERRIDE` to read restricted
project folders. Use `--host-root /srv/projects` to choose another folder,
`--no-host-root` to disable that mount, or `--no-host-root-privileged` to run
the panel as uid 10001. Reinstalls preserve the saved access settings.

Choose the port and the IP it listens on in that same single command. The
`-s --` is what hands the options to the installer instead of to `sh`:

```bash
curl -fsSL https://raw.githubusercontent.com/joseHbernardino/pulso/main/scripts/get-pulso.sh | sudo sh -s -- --port 8080 --ip 10.0.0.5
```

Environment variables do the same, which suits automation:

```bash
curl -fsSL https://raw.githubusercontent.com/joseHbernardino/pulso/main/scripts/get-pulso.sh | PULSO_PORT=8080 PULSO_BIND=10.0.0.5 sudo -E sh
```

`--ip` and `--bind` are the same option. The default is `0.0.0.0`; give a
private address, or `127.0.0.1`, to keep the panel off the network. Re-running
the command upgrades an existing installation in place and keeps the port and
address you chose before.

There is no default password. The panel prints a **first-access code** when the
installation finishes, and that code is what creates the administrator account.
It is shown once, so if it scrolls away:

```bash
sh get-pulso.sh --token        # the pending first-access code, nothing else
```

The code lives inside the panel container at
`/var/lib/pulso/security/setup-token`, accessible to the user that runs the
panel, and stops existing once the administrator is created.

Once installed, the script is also usable from the host:

```bash
sh get-pulso.sh --status       # installation state, plus a pending code
sh get-pulso.sh --local        # air-gapped, image already loaded
sh get-pulso.sh --uninstall    # remove containers, keep the data volumes
```

### Build from source

To work on Pulso itself, build this source tree with Go 1.26.11 or newer:

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

## Docker

Use the assisted installation to preserve Compose settings and manage independent
application containers from the dashboard:

```bash
bash scripts/pulso-docker.sh install
# Existing projects spread across the server:
# bash scripts/pulso-docker.sh install --host
```

The **Projects** page supports ready-made images, Node.js, Python and Dockerfile
builds, with environment variables, persistent storage and CPU/memory limits per
application. Existing processes remain available under **Processes**. See the
[project deployment guide](docs/projects.md) for installation, upgrades and limits.

The dashboard displays the installed version. Administrators can enable
[container image updates from the portal](docs/updates.md), including health checks
and automatic rollback when the replacement fails.

Run Pulso as a Docker service with persistent accounts, process recovery and
application files. The image includes Python 3 and the Node.js version pinned in
the Dockerfile. Applications run inside the Pulso container, with projects mounted
at `/apps`.

```bash
cp docker.env.example .env
sudo install -d -m 0750 -o 10001 -g 10001 applications
docker compose -f compose.yaml -f compose.build.yaml up -d --build
```

The dashboard is exposed on port `5011`. Release tags publish multi-platform
images to GHCR; once published, updates use `docker compose pull pulso` followed
by `docker compose up -d`. See the [Docker deployment guide](docs/docker.md) for
registry installation, RHEL/SELinux, application paths and persistence. For
projects spread across the server, the optional `compose.host.yaml` mounts the
host at `/host` with administrator access; see the guide before enabling it.

## Daemon

You can also create applications in the browser: open `pulso web`, click
**Nova aplicação**, and enter a program or script already on the server. The
form supports runtime selection, working directories, arguments, environment
variables and restart policies. Optionally set a `.env` path to load or create the file in
the panel. Use **Editar** to save application settings and dotenv changes, then
restart the application to apply them. Dotenv editing is restricted to admins.
The daemon dashboard requires login and stores
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

- [Installation](docs/install.md) — one-line installer, distributions, closed networks
- [Observability](docs/observability.md) — container metrics, audit log, notifications
- [Docker inventory](docs/docker-inventory.md) — manage every Engine resource, ownership rules, disk cleanup
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

See [Linux startup and automatic recovery](docs/boot-recovery.md) for systemd installation and recovery behavior.
