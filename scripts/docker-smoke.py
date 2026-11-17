#!/usr/bin/env python3
"""Exercise the built image with disposable containers and volumes only."""
import http.cookiejar
import json
import subprocess
import sys
import time
import urllib.error
import urllib.request
import uuid


def docker(*args, **kwargs):
    return subprocess.run(
        ["docker", *args], check=True, text=True, capture_output=True, **kwargs
    ).stdout.strip()


def wait_until(check, description, timeout=45):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            result = check()
            if result:
                return result
        except (OSError, urllib.error.URLError, subprocess.CalledProcessError):
            pass
        time.sleep(0.2)
    raise RuntimeError("timeout: " + description)


def main():
    image = sys.argv[1] if len(sys.argv) > 1 else "pulso:smoke"
    name = "pulso-smoke-" + uuid.uuid4().hex[:12]
    data_volume, apps_volume = name + "-data", name + "-apps"
    cookies = http.cookiejar.CookieJar()
    client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(cookies))
    base = ""

    def api(method, path, body=None):
        data = json.dumps(body).encode() if body is not None else None
        request = urllib.request.Request(
            base + path, data=data, method=method,
            headers={"Content-Type": "application/json"},
        )
        with client.open(request, timeout=5) as response:
            return json.load(response)

    def launch():
        nonlocal base
        docker(
            "run", "-d", "--name", name,
            "--publish", "127.0.0.1::9615",
            "--env", "PULSO_HOST_APPS_DIR=/host/services",
            "--health-interval", "1s", "--health-start-period", "1s",
            "--mount", "type=volume,source=" + data_volume + ",target=/var/lib/pulso",
            "--mount", "type=volume,source=" + apps_volume + ",target=/apps",
            "--read-only", "--tmpfs", "/tmp:rw,nosuid,size=128m",
            "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", image,
        )
        info = json.loads(docker("inspect", name))[0]
        port = info["NetworkSettings"]["Ports"]["9615/tcp"][0]["HostPort"]
        base = "http://127.0.0.1:" + port
        wait_until(lambda: api("GET", "/api/auth/status"), "web dashboard")
        assert docker("exec", name, "id", "-u") == "10001", "image must run unprivileged"
        assert docker("exec", name, "node", "--version").startswith("v24.")
        assert docker("exec", name, "python3", "--version").startswith("Python 3.")
        wait_until(
            lambda: json.loads(docker("inspect", name))[0]["State"]["Health"]["Status"] == "healthy",
            "container health check", timeout=65,
        )

    def remove(graceful=True):
        if graceful:
            docker("stop", "--time", "30", name)
            assert json.loads(docker("inspect", name))[0]["State"]["ExitCode"] == 0
        else:
            docker("kill", "--signal", "KILL", name)
        docker("rm", name)

    def marker(want):
        return docker("exec", name, "cat", "/apps/observed.txt") == want

    try:
        launch()
        status = api("GET", "/api/auth/status")
        assert status["setup_required"], "new volume should require administrator setup"
        token = docker("exec", name, "cat", "/var/lib/pulso/security/setup-token")
        password = "Smoke-A1!" + uuid.uuid4().hex
        api("POST", "/api/auth/setup", {"token": token, "username": "smokeadmin", "password": password})
        api("POST", "/api/auth/login", {"username": "smokeadmin", "password": password})
        # Failure diagnostics must work even when stdout is empty or output has no newline.
        for app_name, command, code, message in [
            ("stderr-crash", "printf 'missing runtime: python3.11' >&2; exit 127", 127, "missing runtime: python3.11"),
            ("silent-crash", "exit 42", 42, ""),
        ]:
            failed = api("POST", "/api/v1/processes", {
                "name": app_name, "runtime": "process", "entrypoint": "sh", "args": ["-c", command],
                "cwd": "/apps", "restart_policy": "never",
            })
            failure_path = "/api/v1/processes/" + failed["id"]
            wait_until(lambda: api("GET", failure_path).get("last_failure"), "recorded failure")
            details = api("GET", failure_path + "/logs")
            assert details["process"]["exit_code"] == code
            assert details["process"]["last_failure"]["exit_code"] == code
            assert details["stdout"] == "" and message in details["logs"]
            api("DELETE", failure_path)
        restarted = api("POST", "/api/v1/processes", {
            "name": "restart-once", "runtime": "process", "entrypoint": "sh",
            "args": ["-c", "if [ ! -f /apps/restart-marker ]; then touch /apps/restart-marker; exit 7; fi; exec sleep 3600"],
            "cwd": "/apps", "restart_policy": "on-failure", "min_uptime": 1000000000,
        })
        restart_path = "/api/v1/processes/" + restarted["id"]
        wait_until(lambda: api("GET", restart_path).get("last_event") == "restarted", "automatic restart")
        time.sleep(0.5)
        running = api("GET", restart_path)
        assert running["state"] == "running" and running["restarts"] == 1
        assert running["last_failure"]["exit_code"] == 7
        api("DELETE", restart_path)
        # All values and credentials below are disposable test data.
        script = (
            "import os,time\nfrom pathlib import Path\n"
            "Path('/apps/observed.txt').write_text(os.getenv('MARKER','missing')+'|'+os.getenv('EXPLICIT',''))\n"
            "time.sleep(3600)\n"
        )
        docker("exec", "-i", name, "python3", "-c",
               "import sys;from pathlib import Path;Path('/apps/first').mkdir(exist_ok=True);Path('/apps/first/app.py').write_text(sys.stdin.read())", input=script)
        app = api("POST", "/api/v1/processes", {
            "name": "python-env", "runtime": "python", "entrypoint": "/host/services/first/app.py", "cwd": "/host/services/first",
            "env_file": "/host/services/first/app.env", "env_content": "MARKER=first\n", "env_revision": "missing",
            "restart_policy": "never",
        })
        assert app["config"]["cwd"] == "/apps/first"
        assert app["config"]["env_file"] == "/apps/first/app.env"
        listing = api("POST", "/api/v1/filesystem/browse", {"path": "/host/services/first"})
        assert listing["path"] == "/apps/first"
        assert {entry["name"] for entry in listing["entries"]} >= {"app.py", "app.env"}
        try:
            api("POST", "/api/v1/env-files/preview", {"path": "/unmounted/path/.env"})
            raise AssertionError("unmounted host path unexpectedly available")
        except urllib.error.HTTPError as error:
            assert json.load(error)["code"] == "container_path_unavailable"
        stopped = api("POST", "/api/v1/processes", {
            "name": "kept-stopped", "runtime": "process", "entrypoint": "sleep",
            "args": ["3600"], "cwd": "/apps", "restart_policy": "never",
        })
        api("POST", "/api/v1/processes/" + stopped["id"] + "/stop")
        wait_until(lambda: marker("first|"), "initial environment")
        settings_path = "/api/v1/processes/" + app["id"] + "/settings"
        settings = api("GET", settings_path)
        cfg = settings["config"]
        cfg.update(cfg["launch"])
        cfg.update({"revision": settings["revision"], "env_revision": settings["env_file"]["revision"],
                    "env_content": "# preserved comment\nMARKER=second\n"})
        saved = api("PATCH", settings_path, cfg)
        assert saved["pending_changes"] and marker("first|"), "save restarted the application"
        api("POST", "/api/v1/processes/" + app["id"] + "/restart")
        wait_until(lambda: marker("second|"), "manual restart applied dotenv")
        # Save a pending command/environment change, then replace the container.
        settings = api("GET", settings_path)
        cfg = settings["config"]
        cfg.update(cfg["launch"])
        cfg.update({"env": {"EXPLICIT": "saved"}, "revision": settings["revision"],
                    "env_revision": settings["env_file"]["revision"], "env_content": "MARKER=third\n"})
        api("PATCH", settings_path, cfg)
        remove()
        launch()
        status = api("GET", "/api/auth/status")
        assert not status["setup_required"] and status["authenticated"], "accounts or sessions lost"
        wait_until(lambda: marker("third|saved"), "replacement restored pending settings")
        apps = {p["name"]: p for p in api("GET", "/api/v1/processes")}
        assert apps["kept-stopped"]["state"] == "stopped", "stopped application unexpectedly started"
        assert apps["python-env"]["state"] == "running"
        assert not apps["python-env"]["pending_changes"]
        # An ungraceful container loss must recover the last acknowledged state too.
        remove(graceful=False)
        launch()
        assert api("GET", "/api/auth/status")["authenticated"]
        apps = {p["name"]: p for p in api("GET", "/api/v1/processes")}
        assert apps["kept-stopped"]["state"] == "stopped"
        assert apps["python-env"]["state"] == "running"
        wait_until(lambda: marker("third|saved"), "recovery after SIGKILL")
        remove()
        print(json.dumps({"image": image, "non_root": True, "login_persisted": True,
                          "dotenv_editor": True, "host_path_mapping": True, "file_browser": True, "replacement_recovery": True, "crash_recovery": True, "stderr_diagnostics": True, "automatic_restart": True}))
    except Exception:
        logs = subprocess.run(["docker", "logs", "--tail", "50", name], capture_output=True, text=True)
        print(logs.stdout + logs.stderr, file=sys.stderr)
        raise
    finally:
        subprocess.run(["docker", "rm", "--force", name], capture_output=True)
        for volume in (data_volume, apps_volume):
            subprocess.run(["docker", "volume", "rm", volume], capture_output=True)


if __name__ == "__main__":
    main()
