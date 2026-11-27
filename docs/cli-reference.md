# CLI Reference

Complete reference for all Pulso CLI commands, flags, and usage examples.

## Global Flags

| Flag         | Short | Default        | Description                                 |
| ------------ | ----- | -------------- | ------------------------------------------- |
| `--config`   |       | `./pulso.yaml` | Path to configuration file                  |
| `--verbose`  | `-v`  | `false`        | Enable debug-level output                   |
| `--debug`    |       | `false`        | Enable trace-level logging with caller info |
| `--dry-run`  |       | `false`        | Show what would happen without executing    |
| `--no-color` |       | `false`        | Disable colored output                      |
| `--data-dir` |       | `~/.pulso`     | Data directory for state, logs, and sockets |

Pulso searches for config files in this order: `pulso.yaml`, `pulso.yml`, `pulso.json`, `pulso.toml`.

## Process Management

### `pulso start`

Start a managed process. Pulso auto-detects the runtime based on the entrypoint and project files.

```bash
pulso start app.py
pulso start server.go
pulso start index.ts
pulso start dev --runtime bun
```

**Flags:**

| Flag               | Short | Default                 | Description                                                              |
| ------------------ | ----- | ----------------------- | ------------------------------------------------------------------------ |
| `--only`           |       |                         | Start only specific processes by name (comma-separated, requires config) |
| `--name`           | `-n`  | entrypoint filename     | Process name                                                             |
| `--runtime`        | `-r`  | auto-detect             | Runtime: `go`, `python`, `node`, `bun`, `deno`, `ruby`, `php`, `shell`, `auto` |
| `--cwd`            | `-d`  | current directory       | Working directory                                                        |
| `--env`            | `-e`  |                         | Environment variable (`KEY=VAL`, repeatable)                             |
| `--watch`          | `-w`  | `false`                 | Watch for file changes and auto-restart                                  |
| `--watch-ignore`   |       |                         | Glob patterns to ignore when watching (repeatable)                       |
| `--restart-policy` |       | `on-failure`            | Restart policy: `always`, `on-failure`, `never`                          |
| `--max-restarts`   |       | `0` (uses default `10`) | Max restarts within the restart window                                   |
| `--max-retry`      |       |                         | Alias for `--max-restarts`                                               |
| `--instances`      |       | `1`                     | Number of instances to start                                             |
| `--namespace`      |       |                         | Process namespace                                                        |
| `--use-bundle`     |       | `false`                 | Wrap entrypoint with `bundle exec` (Ruby)                                |
| `--script`         |       |                         | Inline shell script to supervise (implies `--runtime shell`)             |
| `--shell`          |       | bash, else sh           | Interpreter for `--runtime shell`                                        |

**Examples:**

```bash
# Start a Go server with a custom name
pulso start ./cmd/api --name api-server

# Start a Python worker with environment variables
pulso start worker.py --name worker -e DB_HOST=localhost -e DB_PORT=5432

# Start a Node.js app in watch mode
pulso start index.ts --watch --watch-ignore "dist/*" --watch-ignore "*.test.ts"

# Start with a specific runtime and restart policy
pulso start main.go --runtime go --restart-policy always --max-restarts 5

# Start with a custom working directory
pulso start app.py --cwd /path/to/project

# Start a shell command line: pipes and operators are preserved
pulso start --runtime shell "node server.js | tee -a /var/log/app.log"

# Start a .sh file, passing its own arguments after --
pulso start --runtime shell /srv/minha-api/start.sh -- --port 8080

# Supervise an inline script, with no entrypoint
pulso start --name backup --script 'pg_dump app | gzip > /bak/app.sql.gz'
```

With `--runtime shell` the entrypoint is handed to a shell instead of being
executed directly, so a quoted command string is kept whole rather than split
into a program and its arguments. See
[Shell commands and scripts](shell-commands.md).

### `pulso stop`

Stop one or more managed processes. Accepts a process ID, name, or `all`.

```bash
pulso stop api-server
pulso stop abc123
pulso stop all
```

**Flags:**

| Flag         | Short | Default | Description                       |
| ------------ | ----- | ------- | --------------------------------- |
| `--force`    |       | `false` | Force stop with SIGKILL           |
| `--timeout`  |       | `5s`    | Grace period before force killing |
| `--graceful` |       | `false` | Graceful stop                     |
| `--format`   | `-f`  | `text`  | Output format: `text`, `json`     |

**Examples:**

```bash
# Graceful stop (SIGTERM, waits 5 seconds)
pulso stop api-server

# Force stop immediately
pulso stop api-server --force

# Stop with a custom grace period
pulso stop api-server --timeout 30s
```

### `pulso restart`

Restart a managed process. Accepts a process ID, name, or `all`.

```bash
pulso restart api-server
pulso restart all
```

Restart applies exponential backoff if the process has accumulated prior restarts. Pre-restart and post-restart hooks are executed.

### `pulso reload`

Graceful reload of a process. Similar to restart but fires reload-specific hooks instead of restart hooks. Does not apply backoff delay.

```bash
pulso reload api-server
```

Use reload when you want a clean stop/start cycle without backoff penalties, typically after configuration changes.

### `pulso delete`

Stop and remove a process from the process table. Accepts a process ID, name, or `all`.

```bash
pulso delete api-server
pulso delete all
```

**Flags:**

| Flag      | Default | Description                |
| --------- | ------- | -------------------------- |
| `--force` | `false` | Force stop before deleting |

### `pulso list`

Display the process table. Alias: `ls`.

```bash
pulso list
pulso ls
```

**Flags:**

| Flag          | Short | Default | Description                                                             |
| ------------- | ----- | ------- | ----------------------------------------------------------------------- |
| `--format`    | `-f`  | `table` | Output format: `table`, `json`, `yaml`                                  |
| `--filter`    | `-F`  |         | Filter by state: `running`, `stopped`, `crashed`, `errored`, `all`      |
| `--namespace` | `-N`  |         | Filter by namespace                                                     |
| `--runtime`   | `-r`  |         | Filter by runtime: `go`, `python`, `node`, `bun`, `deno`, `ruby`, `php` |
| `--tag`       |       |         | Filter by tag                                                           |

**Examples:**

```bash
# Table format (default)
pulso list

# JSON output for scripting
pulso list --format json

# Show only crashed processes
pulso list --filter crashed

# YAML output with filter
pulso list -f yaml -F running
```

### `pulso status`

Show detailed status for a specific process.

```bash
pulso status api-server
```

**Flags:**

| Flag       | Short | Default | Description                    |
| ---------- | ----- | ------- | ------------------------------ |
| `--format` | `-f`  | `table` | Output format: `table`, `json` |

### `pulso logs`

View or stream process logs. Follows by default (like `tail -f`). Alias: `log`.

```bash
pulso logs api-server
pulso logs api-server --nostream
```

**Flags:**

| Flag         | Short | Default | Description                        |
| ------------ | ----- | ------- | ---------------------------------- |
| `--nostream` |       | `false` | Print a snapshot without following |
| `--lines`    | `-n`  | `50`    | Number of recent lines to show     |
| `--err`      |       | `false` | Show only stderr output            |
| `--out`      |       | `false` | Show only stdout output            |

**Examples:**

```bash
# Follow live output (default)
pulso logs api-server

# Print snapshot without following
pulso logs api-server --nostream

# Show last 200 lines
pulso logs api-server -n 200 --nostream

# Show only stderr
pulso logs api-server --err

# Show only stdout
pulso logs api-server --out
```

### `pulso inspect`

Show full process details including configuration, state, recent logs, and resource metrics.

```bash
pulso inspect api-server
```

**Flags:**

| Flag       | Short | Default | Description                                    |
| ---------- | ----- | ------- | ---------------------------------------------- |
| `--format` | `-f`  | `table` | Output format: `table`, `json`                 |
| `--logs`   |       | `20`    | Number of recent log lines to include          |
| `--tui`    |       | `false` | Open interactive TUI control panel for process |

## Commands

### `pulso exec`

Run a one-off command or script through a shell, stream its output, and exit
with the command's own status.

```bash
pulso exec "docker ps --format '{{.Names}}' | sort"
pulso exec --file /srv/minha-api/deploy.sh
cat deploy.sh | pulso exec --file -
pulso exec 'echo "ambiente: $1"' production
pulso exec --app api "rails db:migrate"
pulso exec --output json "uptime"
```

**Flags:**

| Flag         | Short | Default        | Description                                             |
| ------------ | ----- | -------------- | ------------------------------------------------------- |
| `--cwd`      |       | current folder | Working directory                                       |
| `--env`      | `-e`  |                | Environment variable (`NAME=value`, repeatable)         |
| `--env-file` |       |                | Dotenv file loaded before `--env`                       |
| `--shell`    |       | bash, else sh  | Interpreter                                             |
| `--file`     | `-f`  |                | Read the script from a file, or `-` for standard input  |
| `--timeout`  |       | `1m`           | Kill the command after this long (ceiling: `30m`)       |
| `--app`      |       |                | Borrow an application's directory, environment and .env |
| `--quiet`    | `-q`  | `false`        | Do not stream the output                                |
| `--output`   | `-o`  | `text`         | Output format: `text` or `json`                         |

The first argument is the command; the rest become its positional parameters
(`"$1"`, `"$2"`, `"$@"`). With `--app`, an explicit `--cwd` or `--env` wins
over the application's own values. `--dry-run` prints the resolved directory
and command without running it. A command that reaches its timeout has its
whole process group ended.

**Exit code:** the command's own. A failure to start the command at all is
reported as a Pulso error with exit code 1.

## Persistence

### `pulso save`

Persist the current process table to disk. Processes can be restored later with `resurrect`.

```bash
pulso save
```

State is written to `{data-dir}/dump.json` using atomic write (write to temp file, then rename).

### `pulso resurrect`

Restore previously saved processes. Reads from `dump.json` and starts each saved process.

```bash
pulso resurrect
```

Typical workflow for persistence across restarts:

```bash
# Before shutdown
pulso save

# After reboot or daemon restart
pulso resurrect
```

## Daemon

### `pulso daemon`

Manage the background daemon. The daemon provides persistent process management via HTTP-over-Unix-socket IPC.

**Subcommands:**

| Subcommand             | Description                                     |
| ---------------------- | ----------------------------------------------- |
| `pulso daemon start`   | Start the daemon in the background              |
| `pulso daemon stop`    | Stop the daemon (SIGTERM, 10s grace period)     |
| `pulso daemon status`  | Show daemon status (PID, socket path)           |
| `pulso daemon restart` | Restart the daemon                              |
| `pulso daemon reload`  | Reload daemon config without stopping processes |
| `pulso daemon run`     | Run daemon in foreground (used internally)      |

**Examples:**

```bash
# Start daemon
pulso daemon start

# Check status
pulso daemon status

# Restart daemon
pulso daemon restart

# Stop daemon
pulso daemon stop
```

The CLI automatically starts the daemon when needed. If the daemon is not running, commands fall back to direct mode (in-process supervisor).

## Dashboards

### `pulso tui`

Launch the interactive terminal UI dashboard built with BubbleTea.

```bash
pulso tui
```

**Keybindings:**

| Key            | Action                   |
| -------------- | ------------------------ |
| `Up`/`Down`    | Navigate process list    |
| `Enter`        | View process details     |
| `l`            | View process logs        |
| `r`            | Restart selected process |
| `s`            | Stop selected process    |
| `d`            | Delete selected process  |
| `?`            | Show help                |
| `Ctrl+R`       | Refresh process list     |
| `q` / `Ctrl+C` | Quit                     |

The TUI auto-refreshes every 2 seconds.

### `pulso web`

Launch the web-based dashboard with live WebSocket updates.

```bash
pulso web
```

**Flags:**

| Flag       | Default          | Description                |
| ---------- | ---------------- | -------------------------- |
| `--listen` | `localhost:9615` | Address to listen on       |
| `--open`   | `false`          | Open browser automatically |

**Examples:**

```bash
# Launch on default address
pulso web

# Custom port
pulso web --listen 0.0.0.0:8080

# Launch and open browser
pulso web --open
```

## Cron

### `pulso cron`

Manage cron jobs defined in the configuration file.

**Subcommands:**

| Subcommand                | Description                   |
| ------------------------- | ----------------------------- |
| `pulso cron list`         | List all configured cron jobs |
| `pulso cron start <name>` | Enable a cron job             |
| `pulso cron stop <name>`  | Disable a cron job            |
| `pulso cron run <name>`   | Manually trigger a cron job   |

**`pulso cron list` flags:**

| Flag       | Short | Default | Description                    |
| ---------- | ----- | ------- | ------------------------------ |
| `--format` | `-f`  | `table` | Output format: `table`, `json` |

**Examples:**

```bash
# List all cron jobs
pulso cron list

# Manually trigger a job
pulso cron run cleanup

# Disable a job
pulso cron stop cleanup

# Re-enable a job
pulso cron start cleanup
```

## Watch

### `pulso watch`

Enable file watching on an already-running process. When files change, the process is automatically restarted.

```bash
pulso watch api-server
```

**Flags:**

| Flag         | Default           | Description                          |
| ------------ | ----------------- | ------------------------------------ |
| `--paths`    | process cwd       | Paths to watch (repeatable)          |
| `--ignore`   | built-in defaults | Glob patterns to ignore (repeatable) |
| `--debounce` | `100ms`           | Debounce duration                    |

**Default ignore patterns:** `.git`, `node_modules`, `__pycache__`, `*.pyc`, `.DS_Store`, `vendor`, `dist`, `build`, `bin`

**Examples:**

```bash
# Watch with default settings
pulso watch api-server

# Watch specific paths
pulso watch api-server --paths ./src --paths ./internal

# Custom debounce and ignore patterns
pulso watch api-server --debounce 500ms --ignore "*.test.go" --ignore "docs/*"
```

## Diagnostics

### `pulso doctor`

Run diagnostic checks on your environment.

```bash
pulso doctor
```

**Flags:**

| Flag       | Short | Default | Description                   |
| ---------- | ----- | ------- | ----------------------------- |
| `--format` | `-f`  | `text`  | Output format: `text`, `json` |

Checks:

- Available runtimes (Go, Python, Node.js, Bun, Deno, Ruby, PHP)
- Data and runtime directories
- Daemon status and socket accessibility
- File permissions

### `pulso startup`

Install or uninstall Pulso as a system service for boot-time startup.

```bash
pulso startup
```

**Flags:**

| Flag          | Default     | Description                       |
| ------------- | ----------- | --------------------------------- |
| `--platform`  | auto-detect | Init system: `systemd`, `launchd` |
| `--uninstall` | `false`     | Uninstall the startup service     |

**Examples:**

```bash
# Install as system service (auto-detects systemd or launchd)
pulso startup

# Install for systemd explicitly
pulso startup --platform systemd

# Uninstall
pulso startup --uninstall
```

## Updates

### `pulso update`

Update the Pulso binary from GitHub releases. Verifies SHA256 checksums.

```bash
pulso update
```

**Flags:**

| Flag        | Default | Description                               |
| ----------- | ------- | ----------------------------------------- |
| `--check`   | `false` | Only check for updates without installing |
| `--version` |         | Install a specific version                |

**Examples:**

```bash
# Check for updates
pulso update --check

# Update to latest
pulso update

# Install a specific version
pulso update --version v0.2.0
```

### `pulso version`

Print the current version.

```bash
pulso version
```

**Flags:**

| Flag        | Default | Description                                          |
| ----------- | ------- | ---------------------------------------------------- |
| `--verbose` | `false` | Show build details (build time, Go version, OS/Arch) |

## MCP

### `pulso mcp`

Start the MCP (Model Context Protocol) server for AI agent integration.

```bash
pulso mcp
```

**Flags:**

| Flag          | Default          | Description                       |
| ------------- | ---------------- | --------------------------------- |
| `--transport` | `stdio`          | Transport type: `stdio` or `http` |
| `--listen`    | `localhost:8090` | Listen address for HTTP transport |

See the [MCP documentation](mcp.md) for integration details.

## Configuration

### `pulso validate`

Validate a Pulso configuration file.

```bash
pulso validate
pulso validate /path/to/pulso.yaml
```

Checks for duplicate process names, invalid runtimes, missing required fields, and auth configuration errors.

### `pulso config reload`

Reload the daemon's configuration from disk.

```bash
pulso config reload
```

Signals the running daemon to re-read its configuration file.

## Utilities

### `pulso flush`

Flush (clear) log files for a process.

```bash
pulso flush api-server
pulso flush all
```

**Flags:**

| Flag       | Short | Default | Description                   |
| ---------- | ----- | ------- | ----------------------------- |
| `--format` | `-f`  | `text`  | Output format: `text`, `json` |

### `pulso migrate`

Run data migration between Pulso versions.

**Subcommands:**

| Subcommand                 | Description                          |
| -------------------------- | ------------------------------------ |
| `pulso migrate pm2 [file]` | Migrate a PM2 configuration to Pulso |

**`pulso migrate pm2` flags:**

| Flag       | Short | Default  | Description                     |
| ---------- | ----- | -------- | ------------------------------- |
| `--output` | `-o`  |          | Write output to file            |
| `--format` | `-f`  | `"yaml"` | Output format: `yaml` or `json` |

```bash
# Convert a PM2 ecosystem file
pulso migrate pm2 ecosystem.config.js

# Convert and write to file
pulso migrate pm2 ecosystem.config.js -o pulso.yaml

# Output as JSON
pulso migrate pm2 ecosystem.config.js -f json
```

### `pulso events`

Stream real-time process events.

```bash
pulso events
```

**Flags:**

| Flag       | Short | Default | Description                                    |
| ---------- | ----- | ------- | ---------------------------------------------- |
| `--follow` | `-f`  | `false` | Follow the event stream in real time           |
| `--since`  |       | `"1h"`  | Show events since duration (e.g. `10m`, `24h`) |
| `--type`   | `-t`  |         | Filter by event type (e.g. `process.crashed`)  |

### `pulso ready`

Check if a process is ready (healthy and running).

```bash
pulso ready api-server
```

**Flags:**

| Flag        | Default | Description                    |
| ----------- | ------- | ------------------------------ |
| `--timeout` | `60s`   | Maximum time to wait for ready |

## Process Lookup

Commands that accept a process target (`<id|name>`) resolve in this order:

1. **Exact ID match** — the full process UUID
2. **Name match** — the process name
3. **ID prefix match** — a unique prefix of the process ID

If a prefix matches multiple processes, the command fails with an ambiguity error.

## Environment Variables

| Variable       | Description                                                                           |
| -------------- | ------------------------------------------------------------------------------------- |
| `PULSO_*`      | Any config field can be set via `PULSO_` prefix (e.g., `PULSO_DAEMON_LOGLEVEL=debug`) |
| `PULSO_CONFIG` | Path to config file (set by CLI when forking daemon)                                  |
| `PULSO_SHELL`  | Interpreter for the shell runtime, `pulso exec` and the console (default: bash, else sh) |

Pulso also sets two variables in every command it runs through a shell:
`PULSO=true` always, and `PULSO_EXEC=true` for a one-off command, so a script
can tell whether it was invoked by the console or by the supervisor.

## Exit Codes

| Code | Meaning             |
| ---- | ------------------- |
| `0`  | Success             |
| `1`  | General error       |
| `2`  | Command usage error |

`pulso exec` is the exception: it exits with the status of the command it ran,
so it composes with shell conditionals and CI steps.
