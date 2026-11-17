#!/usr/bin/env python3
"""Verify explicit host mode; only disposable fixture paths are read or modified."""
import http.cookiejar
import json
import pathlib
import subprocess
import sys
import tempfile
import urllib.error
import urllib.request
import uuid

from importlib.util import module_from_spec, spec_from_file_location

spec = spec_from_file_location("pulso_docker_smoke", pathlib.Path(__file__).with_name("docker-smoke.py"))
helpers = module_from_spec(spec)
spec.loader.exec_module(helpers)
docker, wait_until = helpers.docker, helpers.wait_until


def main():
    image = sys.argv[1] if len(sys.argv) > 1 else "pulso:smoke"
    name = "pulso-host-smoke-" + uuid.uuid4().hex[:10]
    volume, workspace = name + "-data", name + "-apps"
    # Separate directory trees reproduce projects spread across the server.
    first = pathlib.Path(tempfile.mkdtemp(prefix="pulso-host-test-", dir="/tmp"))
    second = pathlib.Path(tempfile.mkdtemp(prefix="pulso-host-test-", dir="/var/tmp"))
    fixture_paths = [first, second]
    client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    base = ""

    def api(method, path, body=None, opener=client):
        req = urllib.request.Request(base + path, method=method,
                                    data=json.dumps(body).encode() if body is not None else None,
                                    headers={"Content-Type": "application/json"})
        with opener.open(req, timeout=5) as response:
            return json.load(response)

    def launch(host=False):
        nonlocal base
        args = ["run", "-d", "--name", name, "--publish", "127.0.0.1::9615",
                "--health-interval", "1s", "--health-start-period", "1s",
                "--mount", "type=volume,source=" + volume + ",target=/var/lib/pulso",
                "--mount", "type=volume,source=" + workspace + ",target=/apps",
                "--read-only", "--tmpfs", "/tmp:rw,nosuid,size=128m",
                "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true"]
        if host:
            args += ["--user", "0:0", "--env", "PULSO_HOST_ROOT=/host",
                     "--mount", "type=bind,source=/,target=/host",
                     "--security-opt", "label=disable",
                     "--cap-add", "DAC_OVERRIDE", "--cap-add", "CHOWN", "--cap-add", "FOWNER"]
        docker(*args, image)
        inspect = json.loads(docker("inspect", name))[0]
        base = "http://127.0.0.1:" + inspect["NetworkSettings"]["Ports"]["9615/tcp"][0]["HostPort"]
        wait_until(lambda: api("GET", "/api/auth/status"), "host-mode dashboard")

    def remove():
        docker("stop", "--time", "30", name)
        assert json.loads(docker("inspect", name))[0]["State"]["ExitCode"] == 0
        docker("rm", name)

    def marker(path, value):
        return docker("exec", name, "cat", "/host" + str(path / "observed.txt")) == value

    def editable(settings):
        cfg = dict(settings["config"])
        if cfg.get("launch"):
            cfg.update(cfg["launch"])
        cfg["revision"] = settings["revision"]
        return cfg

    try:
        # Start with a regular non-root deployment, then migrate the same data volume.
        launch()
        token = docker("exec", name, "cat", "/var/lib/pulso/security/setup-token")
        password = "Host-Smoke-A1!" + uuid.uuid4().hex
        api("POST", "/api/auth/setup", {"token": token, "username": "hostadmin", "password": password})
        api("POST", "/api/auth/login", {"username": "hostadmin", "password": password})
        api("POST", "/api/v1/processes", {"name": "legacy", "runtime": "process", "entrypoint": "sleep",
                                           "args": ["600"], "cwd": "/apps", "restart_policy": "never"})
        remove()
        launch(host=True)
        assert api("GET", "/api/auth/status")["authenticated"], "existing login/session lost"
        apps = api("GET", "/api/v1/processes")
        assert apps[0]["name"] == "legacy" and apps[0]["state"] == "running"
        listing = api("POST", "/api/v1/filesystem/browse", {"path": "/"})
        assert listing["path"] == "/host" and listing["parent"] == "/host" and listing["host_access"], {key:listing.get(key) for key in ("path","parent","host_access","roots")}
        assert api("POST", "/api/v1/filesystem/browse", {"path": "container:/"})["path"] == "container:/"
        # Create protected files with another owner; edit must preserve ownership and mode.
        setup = """import os,sys
from pathlib import Path
p=Path(sys.argv[1]);p.mkdir(exist_ok=True)
(p/'.env.dev').write_text('MARKER=initial\\n')
(p/'app.py').write_text("import os,time\\nfrom pathlib import Path\\nPath('observed.txt').write_text(os.getenv('MARKER','missing'))\\ntime.sleep(600)\\n")
for f in p.iterdir():os.chown(f,54321,54321);os.chmod(f,0o600)
os.chown(p,54321,54321);os.chmod(p,0o700)
"""
        for index, path in enumerate(fixture_paths):
            assert path.name.startswith("pulso-host-test-") and path.parent in (pathlib.Path("/tmp"), pathlib.Path("/var/tmp"))
            docker("exec", name, "python3", "-c", setup, "/host" + str(path))
            doc = api("POST", "/api/v1/env-files/preview", {"path": str(path / ".env.dev"), "cwd": str(path)})
            assert doc["exists"] and doc["path"] == "/host" + str(path / ".env.dev")
            app = api("POST", "/api/v1/processes", {"name": "host-app-" + str(index), "runtime": "python",
                      "entrypoint": str(path / "app.py"), "cwd": str(path), "env_file": str(path / ".env.dev"),
                      "restart_policy": "never"})
            assert app["config"]["entrypoint"].startswith("/usr/"), "image interpreter replaced with host binary"
            wait_until(lambda: marker(path, "initial"), "host project startup")
            settings_path = "/api/v1/processes/" + app["id"] + "/settings"
            settings = api("GET", settings_path)
            cfg = editable(settings)
            cfg.update({"env_content": "MARKER=edited\n", "env_revision": settings["env_file"]["revision"]})
            saved = api("PATCH", settings_path, cfg)
            assert saved["pending_changes"] and marker(path, "initial")
            stat = json.loads(docker("exec", name, "python3", "-c",
                                    "import os,sys,json;st=os.stat(sys.argv[1]);print(json.dumps([st.st_uid,st.st_gid,st.st_mode&511]))",
                                    "/host" + str(path / ".env.dev")))
            assert stat == [54321, 54321, 0o600], "host file ownership or permissions changed"
            api("POST", "/api/v1/processes/" + app["id"] + "/restart")
            wait_until(lambda: marker(path, "edited"), "edited host environment applied")
        # Image programs and paths round-trip through settings without becoming host paths.
        image_app = api("POST", "/api/v1/processes", {"name": "image-runtime", "runtime": "process",
                         "entrypoint": "/bin/sh", "args": ["-c", "exec sleep 600"],
                         "cwd": "container:/usr", "restart_policy": "never"})
        settings_path = "/api/v1/processes/" + image_app["id"] + "/settings"
        settings = api("GET", settings_path)
        assert settings["config"]["cwd"] == "container:/usr"
        assert settings["config"]["launch"]["entrypoint"] == "container:/bin/sh"
        assert not api("PATCH", settings_path, editable(settings))["pending_changes"]
        # View-only users must not gain filesystem access in full-host mode.
        api("POST", "/api/v1/users", {"username": "hostviewer", "password": password, "role": "view"})
        viewer = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
        api("POST", "/api/auth/login", {"username": "hostviewer", "password": password}, viewer)
        for endpoint in ("/api/v1/filesystem/browse", "/api/v1/env-files/preview"):
            try:
                api("POST", endpoint, {"path": "/"}, viewer)
                raise AssertionError("viewer received host filesystem access")
            except urllib.error.HTTPError as error:
                assert error.code == 403
        remove()
        launch(host=True)
        assert api("GET", "/api/auth/status")["authenticated"]
        assert len(api("GET", "/api/v1/processes")) == 4
        for path in fixture_paths:
            wait_until(lambda: marker(path, "edited"), "host recovery after recreation")
        print(json.dumps({"host_root_listing": True, "separate_directories": True, "dotenv_edit": True,
                          "owner_preserved": True, "image_runtime_preserved": True,
                          "existing_accounts_preserved": True, "viewer_denied": True, "recreation": True}))
    except Exception:
        logs = subprocess.run(["docker", "logs", "--tail", "40", name], text=True, capture_output=True)
        print(logs.stdout + logs.stderr, file=sys.stderr)
        raise
    finally:
        # Cleanup is limited to the randomly named fixture directories.
        cleanup = "import shutil,sys;from pathlib import Path;p=Path(sys.argv[1]);assert p.name.startswith('pulso-host-test-') and p.parent in (Path('/host/tmp'),Path('/host/var/tmp'));shutil.rmtree(p)"
        for path in fixture_paths:
            subprocess.run(["docker", "exec", name, "python3", "-c", cleanup, "/host" + str(path)], capture_output=True)
        subprocess.run(["docker", "rm", "--force", name], capture_output=True)
        for item in (volume, workspace):
            subprocess.run(["docker", "volume", "rm", item], capture_output=True)


if __name__ == "__main__":
    main()
