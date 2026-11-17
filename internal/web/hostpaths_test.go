package web

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pulso.local/pulso/pkg/types"
)

func TestFullHostNamespaces(t *testing.T) {
	resolver := newPathResolver(true, "", "").withHostRoot("/host")
	for _, tc := range []struct{ input, want string }{
		{"/", "/host"}, {"/ctmagent/first/.env.dev", "/host/ctmagent/first/.env.dev"},
		{"/opt/another/.env", "/host/opt/another/.env"}, {"/host/opt/another/.env", "/host/opt/another/.env"},
		{"/apps/legacy/.env", "/apps/legacy/.env"}, {"host:/apps/server/.env", "/host/apps/server/.env"},
		{"container:/etc/pulso/.env", "/etc/pulso/.env"}, {"container:/", "/"},
		{"/ctmagent/../srv/worker/.env", "/host/srv/worker/.env"}, {".env", ".env"},
	} {
		got, err := resolver.translate(tc.input)
		if err != nil || got != tc.want {
			t.Errorf("%s => %s want %s: %v", tc.input, got, tc.want, err)
		}
	}
	if got := resolver.userPath("/usr/bin/python3"); got != "container:/usr/bin/python3" {
		t.Fatal("image runtime ambiguous:", got)
	}
	imageCmd, err := resolver.commandPath("/bin/sh")
	if err != nil || imageCmd != "/bin/sh" {
		t.Fatal("image command was replaced by host command")
	}
	cfg := types.ProcessConfig{Cwd: "/opt/in-image", EnvFile: "/opt/in-image/.env", Entrypoint: "/usr/bin/python3", Launch: &types.LaunchConfig{Entrypoint: "/opt/in-image/app.py"}}
	client := resolver.clientConfig(cfg)
	if client.Cwd != "container:/opt/in-image" || client.Launch.Entrypoint != "container:/opt/in-image/app.py" || cfg.Launch.Entrypoint != "/opt/in-image/app.py" {
		t.Fatal("client namespace or original launch changed")
	}
	for _, path := range []string{"/", "relative", "bad\x00path"} {
		p := newPathResolver(true, "", "").withHostRoot(path)
		if _, err := p.translate("/project"); err == nil {
			t.Fatal("invalid host mount prefix accepted:", path)
		}
	}
	for _, path := range []string{"container:relative", "host:relative"} {
		if _, err := resolver.translate(path); err == nil {
			t.Fatal("relative scoped path accepted")
		}
	}
}

func TestFullHostBrowserAndEnv(t *testing.T) {
	server := setupTestServer(t)
	defer func() { _ = server.supervisor.Shutdown() }()
	// A synthetic mounted root keeps this unit test separate from the real host.
	root := t.TempDir()
	for _, project := range []string{"ctmagent/first", "opt/second"} {
		dir := filepath.Join(root, project)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".env.dev"), []byte("APP=fixture\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	server.paths = newPathResolver(true, "", "").withHostRoot(root)
	handler, err := server.handler()
	if err != nil {
		t.Fatal(err)
	}
	send := func(endpoint string, body any) map[string]any {
		t.Helper()
		data, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", endpoint, strings.NewReader(string(data)))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", endpoint, rec.Code, rec.Body.String())
		}
		var result map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	listing := send("/api/v1/filesystem/browse", map[string]string{"path": "/"})
	if listing["path"] != root || listing["parent"] != root || listing["host_access"] != true {
		t.Fatalf("host root browser: %+v", listing)
	}
	for _, project := range []string{"/ctmagent/first", "/opt/second"} {
		doc := send("/api/v1/env-files/preview", map[string]string{"path": project + "/.env.dev", "cwd": project})
		if doc["path"] != filepath.Join(root, project, ".env.dev") {
			t.Fatalf("host env: %+v", doc)
		}
	}
	image := send("/api/v1/filesystem/browse", map[string]string{"path": "container:/"})
	if image["path"] != "container:/" {
		t.Fatal("container root was redirected to host")
	}
}

func TestHostRootIgnoresNestedKernelAliases(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "sys", "fs", "cgroup")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	resolver := newPathResolver(true, "", "").withHostRoot(root).mergeMounts([]pathMapping{{Host: "/../..", Container: nested, Detected: true}})
	got, err := resolver.translate("/")
	if err != nil || got != root {
		t.Fatalf("host root redirected into a nested system mount: %s %v", got, err)
	}
	parsed := parseProjectMounts(strings.NewReader("15 10 0:1 /../.. /project-cgroup rw - cgroup2 cgroup rw\n"))
	if len(parsed) != 0 {
		t.Fatal("noncanonical mount root accepted as a host path")
	}
}
