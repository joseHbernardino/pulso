#!/usr/bin/env python3
"""Check host access defaults and saved choices using an isolated installation."""
import json
import os
import pathlib
import shutil
import socket
import subprocess
import sys
import tempfile
import urllib.request
import uuid


def main():
    image = sys.argv[1] if len(sys.argv) > 1 else "pulso:ci"
    repository, separator, tag = image.rpartition(":")
    if not separator or "/" in tag or "@" in image:
        raise ValueError("Pass a local image with an explicit tag")
    installer = pathlib.Path(__file__).resolve().with_name("get-pulso.sh")
    fixture = pathlib.Path(tempfile.mkdtemp(prefix="pulso-host-defaults-", dir="/tmp"))
    fixture.chmod(0o755)
    project = "pulso-host-defaults-" + uuid.uuid4().hex[:10]
    panel = project + "-panel"
    conf = fixture / "config"
    conf.mkdir()
    locked = fixture / "locked"
    locked.mkdir(mode=0o700)
    private_file = locked / ".env"
    private_file.write_text("HOST_ACCESS=fixture\n")
    private_file.chmod(0o600)
    public = fixture / "public"
    public.mkdir(mode=0o755)
    public_file = public / ".env"
    public_file.write_text("HOST_ACCESS=fixture\n")
    public_file.chmod(0o644)
    environment = {k: v for k, v in os.environ.items() if not k.startswith("PULSO_")}
    environment["PULSO_CONF_DIR"] = str(conf)
    with socket.socket() as reservation:
        reservation.bind(("127.0.0.1", 0))
        port = reservation.getsockname()[1]
    base = f"http://127.0.0.1:{port}"

    def run(*args, check=True):
        result = subprocess.run(args, env=environment, text=True,
                                stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        if check and result.returncode:
            raise RuntimeError(f"{args[0]} failed:\n{result.stdout}")
        return result.stdout.strip()

    def install(*args):
        run("sh", str(installer), "--project", project, "--local", "--yes", *args)

    def api(path, data):
        request = urllib.request.Request(base + path, data=json.dumps(data).encode(),
                                         headers={"Content-Type": "application/json"})
        with urllib.request.urlopen(request, timeout=30) as response:
            return json.load(response)

    def verify(source, privileged, readable=None):
        container = json.loads(run("docker", "inspect", panel))[0]
        mounts = [m for m in container["Mounts"] if m["Destination"].startswith("/host")]
        if source:
            target = "/host" if source == "/" else "/host" + source
            assert len(mounts) == 1, mounts
            assert mounts[0]["Source"] == source, mounts
            assert mounts[0]["Destination"] == target, mounts
            assert mounts[0]["RW"] is False, mounts
            assert "PULSO_HOST_ROOT=/host" in container["Config"]["Env"]
        else:
            assert not mounts, mounts
            assert not any(v.startswith("PULSO_HOST_ROOT=") for v in container["Config"]["Env"])
        uid = run("docker", "exec", panel, "id", "-u")
        assert uid == ("0" if source and privileged else "10001"), uid
        if readable:
            assert run("docker", "exec", panel, "cat", "/host" + str(readable)) == "HOST_ACCESS=fixture"
        assert run("docker", "exec", panel, "cat", "/var/lib/pulso/host-access-probe") == "preserved"
        assert api("/api/auth/login", {"username": "hostfixture", "password": "HostFixture-2026!Only"})["success"]

    try:
        install("--image", repository, "--tag", tag, "--port", str(port),
                "--bind", "127.0.0.1", "--apps-dir", str(fixture / "apps"))
        token = run("docker", "exec", panel, "cat", "/var/lib/pulso/security/setup-token")
        assert api("/api/auth/setup", {"token": token, "username": "hostfixture",
                                      "password": "HostFixture-2026!Only"})["success"]
        run("docker", "exec", panel, "sh", "-c",
            "umask 077; printf preserved > /var/lib/pulso/host-access-probe")
        verify("/", True, private_file)
        install()
        verify("/", True, private_file)
        print("New installs default to / with root read access; reinstall preserves it.", flush=True)

        install("--host-root", str(public), "--no-host-root-privileged")
        verify(str(public), False, public_file)
        install()
        verify(str(public), False, public_file)
        print("Custom directory and non-root user persist; private state and account survive.", flush=True)

        install("--no-host-root")
        verify("", False)
        install()
        verify("", False)
        print("Explicitly disabled host access stays disabled after reinstall.", flush=True)

        config = conf / (project + ".conf")
        config.write_text("\n".join(line for line in config.read_text().splitlines()
                                    if not line.startswith(("PULSO_HOST_SOURCE=", "PULSO_HOST_PRIVILEGED="))) + "\n")
        install()
        verify("", False)
        install("--host-root", "/", "--host-root-privileged")
        verify("/", True, private_file)
        print("Legacy configurations keep their access; explicit flags enable / again. PASS", flush=True)
    finally:
        for name in (panel, project + "-updater"):
            run("docker", "rm", "-f", name, check=False)
        for name in (project + "_data", project + "_updates"):
            run("docker", "volume", "rm", name, check=False)
        shutil.rmtree(fixture)


if __name__ == "__main__":
    main()
