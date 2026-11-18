#!/usr/bin/env python3
"""Exercise the installer using only a disposable checkout and Compose project."""
import http.cookiejar
import json
import os
import pathlib
import shutil
import subprocess
import tempfile
import time
import urllib.request
import urllib.error
import uuid


def main():
    source = pathlib.Path(__file__).resolve().parent.parent
    fixture = pathlib.Path(tempfile.mkdtemp(prefix="pulso-install-test-", dir="/tmp"))
    project = "pulso-install-test-" + uuid.uuid4().hex[:10]
    image = project + ":fixture"
    environment = dict(os.environ)
    for key in list(environment):
        if key.startswith(("COMPOSE_", "PULSO_")):
            del environment[key]

    def run(*args, check=True):
        result = subprocess.run(args, cwd=fixture, env=environment, text=True,
                                stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        if check and result.returncode:
            raise RuntimeError(f"{' '.join(args)} failed:\n{result.stdout}")
        return result.stdout.strip()

    def manage(*args):
        return run("bash", "scripts/pulso-docker.sh", *args)

    def container():
        return run("docker", "ps", "-aq", "--filter", f"label=com.docker.compose.project={project}",
                   "--filter", "label=com.docker.compose.service=pulso")

    client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    base = ""

    def api(method, path, body=None):
        request = urllib.request.Request(base + path, method=method,
                                         data=json.dumps(body).encode() if body is not None else None,
                                         headers={"Content-Type": "application/json"})
        try:
            with client.open(request, timeout=30) as response:
                return json.load(response)
        except urllib.error.HTTPError as error:
            raise RuntimeError(f"{method} {path}: {error.code}: {error.read().decode()}") from error

    def refresh_address():
        nonlocal base
        state = json.loads(run("docker", "inspect", container()))[0]
        base = "http://127.0.0.1:" + state["NetworkSettings"]["Ports"]["9615/tcp"][0]["HostPort"]

    def application_ready(identifier, phase="ready"):
        deadline = time.monotonic() + 240
        while time.monotonic() < deadline:
            app = api("GET", "/api/v1/container-applications/" + identifier)
            if app["phase"] == phase:
                return app
            if app["phase"] == "failed" and phase != "failed":
                raise RuntimeError(app.get("error", "") + "\n" + app.get("build_log", ""))
            time.sleep(1)
        raise RuntimeError("application deployment timed out")

    try:
        for name in ["Dockerfile", ".dockerignore", "LICENSE", "go.mod", "go.sum", "compose.yaml", "compose.build.yaml", "compose.host.yaml", "compose.update.yaml", "compose.apps.yaml"]:
            shutil.copy2(source / name, fixture / name)
        for name in ["cmd", "internal", "pkg", "licenses"]:
            shutil.copytree(source / name, fixture / name)
        (fixture / "scripts").mkdir()
        shutil.copy2(source / "scripts/pulso-docker.sh", fixture / "scripts/pulso-docker.sh")
        (fixture / "fixture.yaml").write_text(f"services:\n  pulso:\n    image: {image}\n")
        env = fixture / ".env"
        env.write_text(f"# Keep the installation settings\nCOMPOSE_PROJECT_NAME={project}\n"
                       "COMPOSE_FILE=compose.yaml:compose.build.yaml:fixture.yaml\n"
                       "PULSO_BIND=127.0.0.1\nPULSO_PORT=0\nPULSO_IMAGE=ghcr.io/pulso-test/must-not-pull:latest\n")
        output = manage("install")
        print("Installed from source without pulling the configured registry image.", flush=True)
        assert run("docker", "exec", container(), "id", "-u") == "10001"
        assert (fixture / "applications").stat().st_uid == 10001
        refresh_address()
        token = run("docker", "exec", container(), "cat", "/var/lib/pulso/security/setup-token")
        password = "Installer-Smoke-1!" + uuid.uuid4().hex
        api("POST", "/api/auth/setup", {"token": token, "username": "installeradmin", "password": password})
        api("POST", "/api/auth/login", {"username": "installeradmin", "password": password})
        app = api("POST", "/api/v1/processes", {"name": "preserved", "runtime": "process", "entrypoint": "sleep",
                                               "args": ["600"], "cwd": "/apps", "restart_policy": "never"})
        manage("start", "--host")
        refresh_address()
        assert run("docker", "exec", container(), "id", "-u") == "0"
        assert api("GET", "/api/auth/status")["authenticated"], "session lost on host migration"
        assert api("GET", "/api/v1/processes")[0]["name"] == "preserved"
        assert api("GET", "/api/v1/processes")[0]["state"] == "running"
        print("Accounts, sessions and running applications survived host migration.", flush=True)
        # Reproduce the missing COMPOSE_FILE that previously selected the wrong user/image.
        env.write_text("\n".join(line for line in env.read_text().splitlines() if not line.startswith("COMPOSE_FILE=")) + "\n")
        manage("start")
        refresh_address()
        assert run("docker", "exec", container(), "id", "-u") == "0", "existing host mode lost"
        assert "compose.host.yaml" in env.read_text()
        assert "# Keep the installation settings" in env.read_text()
        assert "PULSO_PORT=0" in env.read_text()
        assert "ghcr.io/pulso-test/must-not-pull:latest" in env.read_text()
        assert api("GET", "/api/auth/status")["authenticated"]
        assert api("GET", "/api/v1/processes")[0]["state"] == "running"
        assert "modo host" in manage("doctor")
        # Inspect actual image runtimes and files, without launching project code.
        script = "from pathlib import Path; p=Path('/apps/inspected'); p.mkdir(); (p/'main.py').write_text('raise RuntimeError(\"must not run\")'); (p/'.env.dev').write_text('SECRET=never-return-this')"
        run("docker", "exec", container(), "python3", "-c", script)
        report = api("POST", "/api/v1/projects/inspect", {"path": "/apps/inspected"})
        assert report["suggestions"][0]["available"], report
        assert report["env_files"] == ["/apps/inspected/.env.dev"]
        assert "never-return-this" not in json.dumps(report)
        assert len(api("GET", "/api/v1/processes")) == 1
        print("Recovered the installation profile and inspected a project without executing it.", flush=True)
        isolated = api("POST", "/api/v1/container-applications", {
            "name": "isolated-image", "image": image, "port": 9615, "memory_mb": 128, "cpus": 0.5,
            "data_path": "/var/lib/pulso", "env": {"PRIVATE_TEST": "not-for-viewers"}})
        ready = application_ready(isolated["id"])
        old_container = ready["container_id"]
        inspect = json.loads(run("docker", "inspect", old_container))[0]
        assert inspect["Config"]["Labels"].get("com.docker.compose.project") != project
        assert inspect["HostConfig"]["Memory"] == 128 << 20
        assert inspect["HostConfig"]["MemorySwap"] == 128 << 20
        assert inspect["HostConfig"]["NanoCpus"] == 500000000
        assert not inspect["HostConfig"]["Privileged"]
        assert all(mount["Type"] == "volume" for mount in inspect["Mounts"])
        assert "env" not in api("GET", "/api/v1/container-applications")["applications"][0]["config"]
        broken = dict(ready["config"])
        broken["command"] = ["version"]
        api("PUT", "/api/v1/container-applications/" + isolated["id"], broken)
        failed = application_ready(isolated["id"], "failed")
        assert failed["container_id"] == old_container
        assert json.loads(run("docker", "inspect", old_container))[0]["State"]["Running"]
        print("Independent image deployment, resource limits and rollback passed.", flush=True)
        program = '''import os,sys
from pathlib import Path
from http.server import HTTPServer,BaseHTTPRequestHandler
p=Path('/data/marker')
if not p.exists(): p.write_text(os.environ['MARKER'])
class Handler(BaseHTTPRequestHandler):
 def do_GET(self):
  self.send_response(200);self.end_headers();self.wfile.write((sys.version.split()[0]+'|'+p.read_text()).encode())
HTTPServer(('0.0.0.0',int(os.environ['PORT'])),Handler).serve_forever()
'''
        run("docker", "exec", container(), "python3", "-c",
            "from pathlib import Path; p=Path('/apps/isolated-python'); p.mkdir(); (p/'main.py').write_text(" + repr(program) + "); (p/'.env').write_text('DO_NOT_COPY=private')")
        cfg = {"name": "isolated-python", "source": {"kind": "python", "version": "3.11", "path": "/apps/isolated-python"},
               "port": 8080, "data_path": "/data", "memory_mb": 128, "cpus": 0.5, "env": {"MARKER": "persistent"}}
        python_app = api("POST", "/api/v1/container-applications", cfg)
        python_app = application_ready(python_app["id"])
        python_id = python_app["container_id"]
        assert "PORT" not in python_app["config"]["env"], "generated port was persisted as a user override"
        assert run("docker", "exec", python_id, "python", "--version").startswith("Python 3.11.")
        assert run("docker", "exec", python_id, "id", "-u") == "10001"
        assert run("docker", "exec", python_id, "sh", "-c", "test ! -e /app/.env && echo excluded") == "excluded"
        assigned_port = python_app["config"]["host_port"]
        with urllib.request.urlopen(f"http://127.0.0.1:{assigned_port}", timeout=10) as response:
            assert response.read().decode().endswith("|persistent")
        cfg = python_app["config"]
        run("docker", "exec", container(), "mv", "/apps/isolated-python", "/apps/isolated-python-offline")
        cfg["env"] = {"MARKER": "must-not-replace-existing-data"}
        cfg["port"] = 9090
        api("PUT", "/api/v1/container-applications/" + python_app["id"], cfg)
        replaced = application_ready(python_app["id"])
        assert replaced["container_id"] != python_id
        assert replaced["config"]["host_port"] == assigned_port
        with urllib.request.urlopen(f"http://127.0.0.1:{assigned_port}", timeout=10) as response:
            assert response.read().decode().endswith("|persistent")
        api("POST", "/api/v1/container-applications/" + python_app["id"] + "/stop", {})
        manage("restart")
        refresh_address()
        python_state = next(app for app in api("GET", "/api/v1/container-applications")["applications"] if app["id"] == python_app["id"])
        assert python_state["state"] == "stopped"
        assert json.loads(run("docker", "inspect", old_container))[0]["State"]["Running"]
        api("POST", "/api/v1/container-applications/" + python_app["id"] + "/start", {})
        running = api("GET", "/api/v1/container-applications/" + python_app["id"])
        run("docker", "rm", "-f", running["container_id"])
        api("PUT", "/api/v1/container-applications/" + python_app["id"], running["config"])
        recovered = application_ready(python_app["id"])
        assert recovered["container_id"] != running["container_id"]
        with urllib.request.urlopen(f"http://127.0.0.1:{assigned_port}", timeout=10) as response:
            assert response.read().decode().endswith("|persistent")
        run("docker", "rm", "-f", recovered["container_id"])
        api("POST", "/api/v1/container-applications/" + python_app["id"] + "/delete", {})
        assert all(app["id"] != python_app["id"] for app in api("GET", "/api/v1/container-applications")["applications"])
        run("docker", "volume", "inspect", project + "-app-" + python_app["id"] + "-data")
        print("Python 3.11: source builds, image reuse without source folder, stable ports/data, stopped state and recovery after container removal. PASS", flush=True)
    finally:
        if (fixture / "compose.yaml").exists():
            current = container()
            if current:
                run("docker", "exec", "--user", "0:0", current, "sh", "-c", "rm -rf /apps/inspected /apps/isolated-python /apps/isolated-python-offline", check=False)
            run("docker", "compose", "-p", project, "-f", "compose.yaml", "-f", "compose.build.yaml",
                "-f", "fixture.yaml", "-f", "compose.host.yaml", "-f", "compose.apps.yaml", "down", "--volumes", "--remove-orphans", check=False)
        for identifier in run("docker", "ps", "-aq", "--filter", "label=io.pulso.application.owner=" + project).split():
            run("docker", "rm", "-f", identifier, check=False)
        for identifier in run("docker", "volume", "ls", "-q", "--filter", "label=io.pulso.application.owner=" + project).split():
            run("docker", "volume", "rm", identifier, check=False)
        run("docker", "network", "rm", project + "-applications", check=False)
        for identifier in run("docker", "images", "--format", "{{.Repository}}:{{.Tag}}", project + "-application").split():
            run("docker", "image", "rm", identifier, check=False)
        run("docker", "image", "rm", image, check=False)
        apps = fixture / "applications"
        if apps.exists():
            apps.rmdir()
        shutil.rmtree(fixture)


if __name__ == "__main__":
    main()
