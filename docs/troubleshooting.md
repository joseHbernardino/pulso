# Troubleshooting

Common issues, debugging steps, and error explanations for Pulso.

## Diagnostics

### `pulso doctor`

Run the built-in diagnostic tool first. It checks:

- Available runtimes (Go, Python, Node.js, Bun, Deno, Ruby, PHP)
- Data and runtime directories
- Daemon status and socket accessibility
- File permissions

```bash
pulso doctor
```

Address any issues reported before investigating further.

### Verbose Mode

Enable debug output with the `--verbose` flag to see internal operations:

```bash
pulso --verbose start app.py
```

This shows IPC requests, state transitions, hook execution, and runtime detection details.

## Common Issues

### Process won't start

**Symptom:** `pulso start app.py` reports failure or the process immediately enters `crashed` or `errored` state.

**Debugging steps:**

1. Check the process logs:

   ```bash
   pulso logs app.py
   pulso logs app.py --err
   ```

2. Verify the entrypoint exists and is executable:

   ```bash
   ls -la app.py
   ```

3. Check runtime detection:

   ```bash
   pulso --verbose start app.py
   ```

   Look for the "detected runtime" log line.

4. Try running the command directly:

   ```bash
   python3 app.py
   ```

   If this fails, the issue is with the application, not Pulso.

5. Check for missing dependencies (e.g., Python venv, node_modules):

   ```bash
   ls venv/bin/python   # Python
   ls node_modules      # Node.js
   ```

6. Verify environment variables:
   ```bash
   pulso inspect app.py
   ```

### Daemon not responding

**Symptom:** Commands hang or report that the daemon is unreachable.

**Debugging steps:**

1. Check daemon status:

   ```bash
   pulso daemon status
   ```

2. Check if the socket file exists:

   ```bash
   ls -la ~/.pulso/tmp/pulso.sock
   ```

3. Check if the PID file matches a running process:

   ```bash
   cat ~/.pulso/tmp/pulso.pid
   ps -p $(cat ~/.pulso/tmp/pulso.pid)
   ```

4. Restart the daemon:

   ```bash
   pulso daemon restart
   ```

5. If restart fails, stop and start fresh:

   ```bash
   pulso daemon stop
   pulso daemon start
   ```

6. Check for stale socket files (daemon crashed without cleanup):
   ```bash
   rm ~/.pulso/tmp/pulso.sock
   pulso daemon start
   ```

### Process keeps restarting (crash loop)

**Symptom:** Process state oscillates between `running` and `crashed`, eventually reaches `errored`.

**Debugging steps:**

1. Check stderr for error messages:

   ```bash
   pulso logs worker --err -n 100
   ```

2. Review restart count and policy:

   ```bash
   pulso inspect worker
   ```

   Check `restarts`, `max_restarts`, and `restart_policy`.

3. The process is likely crashing due to an application error. Fix the root cause.

4. Temporarily prevent auto-restart:
   ```yaml
   restart_policy: "never"
   ```
   Or reduce max restarts:
   ```yaml
   max_restarts: 1
   ```

### Runtime detected incorrectly

**Symptom:** Pulso uses the wrong runtime or interpreter.

**Debugging steps:**

1. Specify the runtime explicitly:

   ```bash
   pulso start app.py --runtime python
   ```

2. Check what runtime is detected:

   ```bash
   pulso --verbose start app.py
   ```

3. Detection priority is: Go → Python → Node.js → Bun → Deno → Ruby → PHP. If multiple markers exist (e.g., both `go.mod` and `package.json`), the first match wins. Use `--runtime` to override.

### Watch not triggering restarts

**Symptom:** File changes are not detected or process is not restarted.

**Debugging steps:**

1. Verify watch is enabled:

   ```bash
   pulso inspect api-server
   ```

   Check the watch configuration section.

2. The changed file might be in an ignored directory. Check ignore patterns:
   - Default ignores: `.git`, `node_modules`, `__pycache__`, `*.pyc`, `.DS_Store`, `vendor`, `dist`, `build`, `bin`

3. The debounce window might be too high. Try reducing it:

   ```bash
   pulso watch api-server --debounce 50ms
   ```

4. Verify the watched paths include your source files:
   ```bash
   pulso watch api-server --paths ./src --paths ./internal
   ```

### Logs not appearing

**Symptom:** `pulso logs` shows no output or stale data.

**Debugging steps:**

1. Verify the process is running:

   ```bash
   pulso list
   ```

2. Check if log files exist:

   ```bash
   ls -la ~/.pulso/apps/{process-name}/
   ```

3. The process might be buffering output. Many runtimes buffer stdout when not connected to a terminal. Set the appropriate unbuffered flag:
   - Python: `PYTHONUNBUFFERED=1`
   - Node.js: `NODE_OPTIONS=--no-buffering`
   - Go: output is unbuffered by default

   ```yaml
   env:
     PYTHONUNBUFFERED: "1"
   ```

4. Check for log rotation — old logs may have been archived:
   ```bash
   ls -la ~/.pulso/apps/{process-name}/stdout.log.*
   ```

### Permission denied on socket

**Symptom:** `permission denied` when connecting to the daemon socket.

**Debugging steps:**

1. Check socket permissions:

   ```bash
   ls -la ~/.pulso/tmp/pulso.sock
   ```

   The socket should be `0o660` (read/write for owner and group).

2. Ensure you are the same user that started the daemon:

   ```bash
   pulso daemon status
   ```

3. Do not start the daemon with `sudo` unless you intend to manage it as root.

### Self-update fails

**Symptom:** `pulso update` reports an error.

**Debugging steps:**

1. Check if you have write permission to the binary:

   ```bash
   which pulso
   ls -la $(which pulso)
   ```

2. If installed system-wide, you may need elevated privileges:

   ```bash
   sudo pulso update
   ```

3. Check network connectivity to GitHub:

   ```bash
   curl -sf "https://api.github.com/repos/${PULSO_RELEASE_REPOSITORY:?Set your publishing repository}/releases/latest" | head -5
   ```

4. Verify checksum integrity. If the download was corrupted, retry:
   ```bash
   pulso update --version v0.2.0
   ```

### Save/resurrect not restoring processes

**Symptom:** `pulso resurrect` does not start saved processes.

**Debugging steps:**

1. Verify the dump file exists:

   ```bash
   ls -la ~/.pulso/dump.json
   ```

2. Check its contents:

   ```bash
   cat ~/.pulso/dump.json | python3 -m json.tool
   ```

3. Ensure entrypoints are still valid. If files were moved or deleted since `save`, resurrect will fail.

4. Check permissions on the dump file and data directory.

## Error States

| State     | Meaning                                  | Resolution                                     |
| --------- | ---------------------------------------- | ---------------------------------------------- |
| `stopped` | Process was stopped normally             | Start it with `pulso start` or `pulso restart` |
| `crashed` | Process exited with a non-zero code      | Check stderr logs for the error                |
| `errored` | Exceeded max restarts or hook failure    | Fix the root cause, then restart               |
| `waiting` | Waiting for backoff delay before restart | Normal transient state during auto-recovery    |

## Getting Help

If the troubleshooting steps above don't resolve your issue:

1. Check the issue tracker of the repository where your copy of Pulso is published
2. Run `pulso doctor` and include the output in your report
3. Include the Pulso version (`pulso version --verbose`) and OS information
4. Include relevant log output (`pulso logs --err`)
