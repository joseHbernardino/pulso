package projects

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPythonDependenciesDocker(t *testing.T) {
	if os.Getenv("PULSO_PROJECT_DOCKER_TEST") != "1" {
		t.Skip("set PULSO_PROJECT_DOCKER_TEST=1 to build disposable Python images")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	dir := t.TempDir()
	prefix := fmt.Sprintf("pulso-python-dependency-test-%d", time.Now().UnixNano())
	docker := func(args ...string) string {
		t.Helper()
		output, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("docker %s: %v\n%s", args[0], err, output)
		}
		return strings.TrimSpace(string(output))
	}
	write := func(name, value string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	build := func(tag string, wantError bool) {
		t.Helper()
		encoded, err := Archive(ctx, dir, "python", "3.11")
		if err != nil {
			t.Fatal(err)
		}
		archive, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatal(err)
		}
		image := prefix + ":" + tag
		t.Cleanup(func() {
			cleanup, done := context.WithTimeout(context.Background(), 30*time.Second)
			defer done()
			_ = exec.CommandContext(cleanup, "docker", "image", "rm", image).Run()
		})
		cmd := exec.CommandContext(ctx, "docker", "buildx", "build", "--load", "--progress=plain", "-f", ".pulso.Dockerfile", "-t", image, "-")
		cmd.Stdin = bytes.NewReader(archive)
		output, err := cmd.CombinedOutput()
		if (err != nil) != wantError {
			t.Fatalf("build %s: %v\n%s", tag, err, output)
		}
		if wantError && !bytes.Contains(output, []byte("changed significantly")) {
			t.Fatalf("build failed for a different reason than a stale lock:\n%s", output)
		}
	}
	write("main.py", "import anyio.to_thread\nfrom importlib.metadata import version\nprint(version('anyio'))\n")
	write("requirements.txt", "anyio==4.11.0\n")
	build("requirements", false)
	if got := docker("run", "--rm", prefix+":requirements"); got != "4.11.0" {
		t.Fatal("requirements dependency unavailable:", got)
	}
	if err := os.Remove(filepath.Join(dir, "requirements.txt")); err != nil {
		t.Fatal(err)
	}
	manifest := "[tool.poetry]\npackage-mode = false\n[tool.poetry.dependencies]\npython = '^3.11'\nanyio = '4.11.0'\n[tool.poetry.group.dev.dependencies]\npytest = '8.4.2'\n"
	write("pyproject.toml", manifest)
	build("poetry-unlocked", false)
	if got := docker("run", "--rm", prefix+":poetry-unlocked"); got != "4.11.0" {
		t.Fatal("Poetry dependency unavailable without lockfile:", got)
	}
	lock := docker("run", "--rm", prefix+":poetry-unlocked", "cat", "poetry.lock")
	write("poetry.lock", lock+"\n")
	// The exported requirements must not replace the authoritative Poetry lock.
	write("requirements.txt", "this-is-an-invalid-stale-export===not-a-version\n")
	build("poetry-locked", false)
	if got := docker("run", "--rm", prefix+":poetry-locked"); got != "4.11.0" {
		t.Fatal("Poetry dependency unavailable:", got)
	}
	if got := docker("run", "--rm", prefix+":poetry-locked", "poetry", "run", "python", "main.py"); got != "4.11.0" {
		t.Fatal("poetry run did not use the application environment:", got)
	}
	if got := docker("run", "--rm", prefix+":poetry-locked", "python", "-c", "import sys,importlib.util; assert sys.prefix=='/opt/venv'; assert importlib.util.find_spec('pytest') is None; print('isolated')"); got != "isolated" {
		t.Fatal("build tools or dev dependencies leaked into application environment:", got)
	}
	if got := docker("run", "--rm", prefix+":poetry-locked", "id", "-u"); got != "10001" {
		t.Fatal("application runs as root:", got)
	}
	// A stale lock must fail the build, rather than silently resolve new versions.
	write("pyproject.toml", strings.Replace(manifest, "'4.11.0'", "'4.10.0'", 1))
	build("stale-lock", true)
}
