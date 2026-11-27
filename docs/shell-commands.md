# Shell Commands and Scripts

Pulso can run anything a shell can run: a command line with pipes and
conditionals, a `.sh` file already on the server, or a script written in the
dashboard. There are two ways to do it, and the difference matters:

| You want                                                  | Use                                  |
| --------------------------------------------------------- | ------------------------------------ |
| A long-running command, supervised, with logs and restart | The **shell runtime** (`pulso start`) |
| To run something once and read its output                 | **`pulso exec`** or the **console**  |

Everything else in Pulso stays the same: a shell application appears in
`pulso list`, has logs, metrics, hooks, watch mode and cron, exactly like a Go
or Python application.

## The shell runtime

`--runtime shell` (aliases: `bash`, `sh`, `zsh`, `ksh`, `script`) hands the
entrypoint to a shell instead of executing it directly. That is what makes
pipes, `&&`, redirections and variable expansion work.

```bash
# A command line
pulso start --runtime shell "node server.js | tee -a /var/log/app.log"

# A script file on the server, with its own arguments after --
pulso start --runtime shell /srv/minha-api/start.sh -- --port 8080

# An inline script: no entrypoint needed
pulso start --name backup --script 'pg_dump app | gzip > /bak/app.sql.gz'
```

### How the three shapes are resolved

| Input                                   | What runs                                  |
| --------------------------------------- | ------------------------------------------ |
| `--script "<script>"`                   | `<shell> -c "<script>" pulso <args...>`    |
| An entrypoint that names a script file  | `<shell> <file> <args...>`                 |
| Any other entrypoint (a command line)   | `<shell> -c "<command> <quoted args>" pulso` |

An entrypoint counts as a script file when it has a shell extension (`.sh`,
`.bash`, `.ksh`, `.zsh`) or is an existing file reached by path. A bare word
such as `uptime` is always a command, never a file.

With `--script`, arguments become the script's positional parameters, so it
reads them as `"$1"`, `"$2"` and `"$@"`. With a command line, arguments are
shell-quoted and appended to it, so a value with spaces stays one word.

### Which shell

In order: `--shell`, then `PULSO_SHELL`, then `bash` if it is installed, then
`sh`. The shell runtime never loads a login profile, so a command behaves the
same in the terminal, in cron and in the panel. If an application needs
`nvm`, `pyenv` or `rbenv`, source it in the script itself.

### In `pulso.yaml`

```yaml
processes:
  - name: "nightly-backup"
    runtime: "shell"
    script: |
      set -euo pipefail
      pg_dump "$DATABASE_URL" | gzip > /bak/app-$(date +%F).sql.gz
      find /bak -name 'app-*.sql.gz' -mtime +14 -delete
    cwd: "/srv/minha-api"
    restart_policy: "never"
    cron_restart: "0 3 * * *"

  - name: "api-com-log"
    runtime: "shell"
    entrypoint: "node server.js 2>&1 | tee -a /var/log/api.log"
    cwd: "/srv/minha-api"
    restart_policy: "always"
```

`script` and `entrypoint` are alternatives; when both are given, `script`
wins. A process with only `script` is valid — the entrypoint requirement is
waived for it.

### In the dashboard

**Nova aplicação** → **Tipo de execução** → **Script bash / comando shell**.
The entrypoint field accepts a command line or a path to a `.sh` file, and a
**Script shell** box appears for an inline script. The **Interpretador** field
chooses the shell. Editing an application shows the command or script as it
was written, not the resolved `sh -c` invocation.

## `pulso exec` — run a command once

```bash
# A command line, with a pipe
pulso exec "docker ps --format '{{.Names}}' | sort"

# A script file, or standard input
pulso exec --file /srv/minha-api/deploy.sh
cat deploy.sh | pulso exec --file -

# Positional parameters
pulso exec 'echo "ambiente: $1"' production

# In an application's own directory, environment and .env
pulso exec --app api "rails db:migrate"

# A machine-readable result
pulso exec --output json "uptime"
```

Output is streamed as it arrives, and Pulso exits with the command's own exit
code, so `pulso exec ... && echo ok` and CI steps work as expected.

**Flags:**

| Flag         | Short | Default         | Description                                            |
| ------------ | ----- | --------------- | ------------------------------------------------------ |
| `--cwd`      |       | current folder  | Working directory                                      |
| `--env`      | `-e`  |                 | Environment variable (`NAME=value`, repeatable)        |
| `--env-file` |       |                 | Dotenv file loaded before `--env`                      |
| `--shell`    |       | bash, else sh   | Interpreter                                            |
| `--file`     | `-f`  |                 | Read the script from a file, or from `-` for stdin     |
| `--timeout`  |       | `1m`            | Kill the command after this long (ceiling: 30m)        |
| `--app`      |       |                 | Borrow an application's directory, environment and .env |
| `--quiet`    | `-q`  | `false`         | Do not stream the output                               |
| `--output`   | `-o`  | `text`          | `text` or `json`                                       |

With `--app`, anything given explicitly wins: `--cwd` overrides the
application's folder, and `--env` overrides its variables.

`--dry-run` prints the resolved working directory and command without running
anything.

A command that exceeds its timeout has its whole process group signalled, so
background children started by a script do not survive it.

## The dashboard console

**Console** in the sidebar runs a command on the server that hosts the panel
and streams the output into the page. It offers a working directory, an
application context (which lends its folder, variables and `.env`), an
interpreter, a timeout, ready-made commands, a per-browser history, and
**Parar comando** to end a run.

A command keeps running if the page is closed or the connection drops — a
migration must not die halfway through. What ends it is its timeout or
**Parar comando**.

### Rules the console enforces

- **Administrators only.** An account with the view role, or no session at
  all, is refused. An installation with no accounts has no administrator, so
  the console refuses there too.
- **Everything is audited.** Each attempt is recorded as `command.exec`, with
  the first line of the command and the account that ran it, including the
  attempts that were refused. See [observability](observability.md).
- **Bounded.** At most four commands run at once; a command may not exceed
  30 minutes; the displayed output is capped (the command still runs).

The console runs where the panel runs. In a Docker installation that is inside
the panel container, which is why `docker` commands there act on the Engine
only when the socket is mounted. To act inside an application's container, use
the container's own actions on the **Projetos** page.

## The HTTP API

```bash
curl -N -X POST http://localhost:5011/api/v1/exec \
  -H 'Content-Type: application/json' \
  -b 'pulso_session=<cookie>' \
  -d '{"command":"df -h","timeout_seconds":60}'
```

The response is NDJSON — one JSON object per line:

| Event    | Fields                                                                |
| -------- | --------------------------------------------------------------------- |
| `start`  | `id`, `shell`, `cwd`                                                  |
| `output` | `stream` (`stdout`/`stderr`), `text`                                  |
| `end`    | `exit_code`, `duration_ms`, `timed_out`, `stopped`, `output_limited`  |
| `error`  | `error` — the command could not be started                            |

`POST /api/v1/exec/{id}/stop`, with the `id` from the `start` event, ends a
running command: the process group is asked to stop, then forced down after
five seconds.

Accepted request fields: `command` (required), `args`, `cwd`, `env`,
`env_file`, `shell`, `timeout_seconds`, `app`.

## Security

A shell command is as powerful as the account that runs Pulso. Three things
follow:

1. **The panel runs commands as its own user.** In the default Docker
   installation that is root inside the panel container, with the host mounted
   read-only. Choose `--no-host-root-privileged` at install time to run as uid
   10001 instead; see the [installation guide](install.md).
2. **Give the view role to anyone who should not run commands.** The role is
   enforced on the server, not only in the interface.
3. **Read the audit log.** `command.exec` entries are the record of who ran
   what, and they are written whether the command succeeded, failed or was
   refused.

Arguments elsewhere in Pulso are still never passed through a shell: the
language runtimes execute a program and an argument vector directly. The shell
runtime, `pulso exec` and the console are the explicit, audited exceptions.

## Related

- [CLI Reference](cli-reference.md) — every command and flag
- [Configuration](configuration.md) — the `pulso.yaml` schema
- [Hooks](hooks.md) — commands run around an application's lifecycle
- [Reload, Watch and Cron](reload-watch-cron.md) — scheduling a shell application
- [Observability](observability.md) — the audit log
- [Access control](access-control.md) — roles and the first administrator
