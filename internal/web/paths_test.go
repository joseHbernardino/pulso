package web

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"pulso.local/pulso/pkg/types"
)

func TestHostPathMapping(t *testing.T) {
	resolver := newPathResolver(true, "/ctmagent/develop/Servicos", `[{"host":"/opt/extra","container":"/extra"}]`)
	for _, tc := range []struct{ input, want string }{
		{"/ctmagent/develop/Servicos/one/.env.dev", "/apps/one/.env.dev"},
		{"/ctmagent/develop/Servicos/two/main.py", "/apps/two/main.py"},
		{"/ctmagent/develop/Servicos", "/apps"},
		{"/ctmagent/develop/Servicos-other/.env", "/ctmagent/develop/Servicos-other/.env"},
		{"/ctmagent/develop/Servicos/../private/.env", "/ctmagent/develop/private/.env"},
		{"/apps/one/.env.dev", "/apps/one/.env.dev"},
		{"/opt/extra/project/.env", "/extra/project/.env"},
		{".env.dev", ".env.dev"}, {"python3", "python3"},
	} {
		got, err := resolver.translate(tc.input)
		if err != nil || got != tc.want {
			t.Errorf("%q => %q want %q (%v)", tc.input, got, tc.want, err)
		}
	}
	// Relative host paths cannot be inferred inside Docker and must not be guessed.
	resolver = newPathResolver(true, "./applications", "")
	if len(resolver.mappings) != 1 || resolver.mappings[0].Host != "" {
		t.Fatal("guessed absolute host path")
	}
	for _, config := range []string{`not json`, `[{"host":"relative","container":"/apps"}]`, `[{"host":"/","container":"/apps"}]`, `[{"host":"/project","container":"/"}]`, `[{"host":"/project","container":"/one"},{"host":"/project","container":"/two"}]`} {
		invalid := newPathResolver(true, "", config)
		if _, err := invalid.translate("/apps/test"); err == nil {
			t.Fatal("invalid mapping accepted:", config)
		}
	}
}

func TestPathDiagnostics(t *testing.T) {
	resolver := newPathResolver(true, "/host/projects", "")
	tests := []struct {
		err  error
		code string
	}{
		{&fs.PathError{Op: "open", Path: "/unmounted/project", Err: fs.ErrNotExist}, "container_path_unavailable"},
		{&fs.PathError{Op: "open", Path: "/apps/project", Err: fs.ErrNotExist}, "path_not_found"},
		{&fs.PathError{Op: "open", Path: "/apps/project", Err: fs.ErrPermission}, "permission_denied"},
		{&fs.PathError{Op: "create", Path: "/apps/.env", Err: syscall.EROFS}, "read_only"},
		{&exec.Error{Name: "missing-runtime", Err: exec.ErrNotFound}, "command_missing"},
	}
	for _, tc := range tests {
		if got := resolver.issue(tc.err); got.Code != tc.code || !got.Container {
			t.Fatalf("issue = %+v", got)
		}
	}
	native := newPathResolver(false, "", "")
	if native.issue(&fs.PathError{Op: "open", Path: "/missing", Err: fs.ErrNotExist}).Code != "path_not_found" {
		t.Fatal("native error misclassified as Docker")
	}
}

func TestBrowseAndMappedPreview(t *testing.T) {
	server := setupTestServer(t)
	defer func() { _ = server.supervisor.Shutdown() }()
	root := t.TempDir()
	server.paths = newPathResolver(true, "", `[{"host":"/host/services","container":`+strconvJSON(root)+`}]`)
	for _, name := range []string{"first", "second"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	envPath := filepath.Join(root, "first", ".env.dev")
	if err := os.WriteFile(envPath, []byte("PRIVATE_CONTENT=never-list-this\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler, err := server.handler()
	if err != nil {
		t.Fatal(err)
	}
	send := func(path string, body map[string]any, want int) []byte {
		t.Helper()
		data, _ := json.Marshal(body)
		request := httptest.NewRequest("POST", path, strings.NewReader(string(data)))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, request)
		if rec.Code != want {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("path metadata can be cached")
		}
		return rec.Body.Bytes()
	}
	body := send("/api/v1/filesystem/browse", map[string]any{"path": "/host/services/first"}, 200)
	if strings.Contains(string(body), "never-list-this") || !strings.Contains(string(body), ".env.dev") {
		t.Fatal("browse leaked content or omitted dotenv")
	}
	var doc struct {
		Path   string `json:"path"`
		Cwd    string `json:"cwd"`
		Mapped bool   `json:"mapped"`
	}
	if err = json.Unmarshal(send("/api/v1/env-files/preview", map[string]any{"path": "/host/services/first/.env.dev", "cwd": "/host/services/first"}, 200), &doc); err != nil {
		t.Fatal(err)
	}
	if !doc.Mapped || doc.Path != envPath || doc.Cwd != filepath.Dir(envPath) {
		t.Fatalf("wrong mapped preview: %+v", doc)
	}
	var issue pathIssue
	if err = json.Unmarshal(send("/api/v1/env-files/preview", map[string]any{"path": "/unmounted/directory/.env"}, 400), &issue); err != nil {
		t.Fatal(err)
	}
	if issue.Code != "container_path_unavailable" {
		t.Fatalf("raw missing-directory error: %+v", issue)
	}
	send("/api/v1/filesystem/browse", map[string]any{"path": root, "offset": -1}, 400)
	// Applications in different subfolders use the same configured host root.
	for _, name := range []string{"first", "second"} {
		cfg, err := server.prepareProcess(types.ProcessConfig{Name: name, Runtime: "process", Entrypoint: "sleep", Args: []string{"60"}, Cwd: "/host/services/" + name})
		if err != nil || cfg.Cwd != filepath.Join(root, name) {
			t.Fatalf("mapped process %s: %+v %v", name, cfg, err)
		}
		proc, err := server.supervisor.AddProcess(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		if proc.GetState() != types.StateRunning {
			t.Fatal("mapped application did not start")
		}
	}
	_, err = server.prepareProcess(types.ProcessConfig{Name: "missing", Runtime: "process", Entrypoint: "pulso-nonexistent-binary", Cwd: root})
	if !errors.Is(err, exec.ErrNotFound) {
		t.Fatal("missing runtime was not caught before launch:", err)
	}
}

func strconvJSON(value string) string { data, _ := json.Marshal(value); return string(data) }

func TestMountDetection(t *testing.T) {
	data := `10 1 8:1 / / rw - ext4 /dev/sda rw
11 10 8:1 /host/services /apps rw - ext4 /dev/sda rw
12 10 8:1 /host/project\040files /projects\040extra ro shared:10 - ext4 /dev/sda rw
13 10 8:1 /var/lib/docker/volumes/pulso/_data /var/lib/pulso rw - ext4 /dev/sda rw
14 10 8:1 /etc/hosts /etc/hosts rw - ext4 /dev/sda rw
15 10 8:1 /var/lib/docker/volumes/apps/_data /named-projects rw - ext4 /dev/sda rw
`
	mounts := parseProjectMounts(strings.NewReader(data))
	if len(mounts) != 3 {
		t.Fatalf("unexpected mount roots: %+v", mounts)
	}
	if mounts[0].Host != "/host/services" || mounts[0].Container != "/apps" {
		t.Fatal(mounts[0])
	}
	if mounts[1].Host != "/host/project files" || mounts[1].Container != "/projects extra" || !mounts[1].ReadOnly {
		t.Fatal("mount escapes/options not decoded")
	}
	if mounts[2].Host != "" {
		t.Fatal("Docker storage path exposed as a project alias")
	}
	root := t.TempDir()
	detected := []pathMapping{{Host: "/detected/root", Container: root, Detected: true, ReadOnly: true}}
	resolver := newPathResolver(true, "", `[{"host":"/configured/root","container":`+strconvJSON(root)+`}]`).mergeMounts(detected)
	got, err := resolver.translate("/configured/root/file.env")
	if err != nil || got != filepath.Join(root, "file.env") {
		t.Fatal("explicit mapping was replaced by mount detection")
	}
	// Without an explicit host alias, a mounted root can be recognized automatically.
	resolver = newPathResolver(true, "", "").mergeMounts(detected)
	got, err = resolver.translate("/detected/root/file.env")
	if err != nil || got != filepath.Join(root, "file.env") {
		t.Fatal("available bind mount was not recognized")
	}
}

func TestKernelMountParser(t *testing.T) {
	data := "10 1 8:1 / / rw - ext4 /dev/sda rw\n" +
		"11 10 8:1 /host/services /apps rw shared:2 - ext4 /dev/sda rw\n" +
		`12 10 8:1 /host/project\040files /extra\040files ro - ext4 /dev/sda rw` + "\n" +
		"13 10 8:1 /var/lib/docker/volumes/test/_data /projects rw - ext4 /dev/sda rw\n" +
		"14 10 8:1 /data/auth /var/lib/pulso rw - ext4 /dev/sda rw\n" +
		"15 10 8:1 /host/etc /etc/hosts rw - ext4 /dev/sda rw\n" +
		"malformed line\n"
	mounts := parseProjectMounts(strings.NewReader(data))
	if len(mounts) != 3 {
		t.Fatalf("unexpected mounts: %+v", mounts)
	}
	if mounts[0].Host != "/host/services" || mounts[0].Container != "/apps" || !mounts[0].Detected {
		t.Fatal(mounts[0])
	}
	if mounts[1].Host != "/host/project files" || mounts[1].Container != "/extra files" || !mounts[1].ReadOnly {
		t.Fatal(mounts[1])
	}
	if mounts[2].Host != "" {
		t.Fatal("named volume exposed as a host project root")
	}
	// Explicit aliases win when mount roots differ from the host filesystem's view.
	target := t.TempDir()
	resolver := newPathResolver(true, "", `[{"host":"/configured","container":`+strconvJSON(target)+`}]`)
	resolver = resolver.mergeMounts([]pathMapping{{Host: "/kernel/filesystem/subvolume", Container: target, Detected: true, ReadOnly: true}})
	got, err := resolver.translate("/configured/app/.env")
	if err != nil || got != filepath.Join(target, "app", ".env") {
		t.Fatal(got, err)
	}
}
