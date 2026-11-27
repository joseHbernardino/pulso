package web

import (
	"strings"
	"testing"

	"pulso.local/pulso/pkg/types"
)

// A shell application is the one case where the panel deliberately hands a
// string to a shell, so what prepareWebProcess produces is worth pinning.
func TestPrepareWebProcessResolvesShellCommandLine(t *testing.T) {
	cfg, err := prepareWebProcess(types.ProcessConfig{
		Name:       "painel-shell",
		Runtime:    "shell",
		Entrypoint: "node server.js | tee app.log",
		Cwd:        t.TempDir(),
	})
	if err != nil {
		t.Fatalf("prepareWebProcess: %v", err)
	}
	if cfg.Runtime != "shell" {
		t.Errorf("Runtime = %q, want shell", cfg.Runtime)
	}
	if !strings.Contains(cfg.Entrypoint, "sh") {
		t.Errorf("Entrypoint = %q, want a shell binary", cfg.Entrypoint)
	}
	if len(cfg.Args) < 2 || cfg.Args[0] != "-c" || cfg.Args[1] != "node server.js | tee app.log" {
		t.Errorf("Args = %q, want the command line passed with -c", cfg.Args)
	}
	if cfg.Launch == nil || cfg.Launch.Entrypoint != "node server.js | tee app.log" {
		t.Errorf("Launch = %+v, want the typed command preserved for editing", cfg.Launch)
	}
}

func TestPrepareWebProcessAcceptsAnInlineScriptWithoutAnEntrypoint(t *testing.T) {
	// The trailing newline is trimmed when the script is stored, so compare
	// against what the panel keeps.
	script := "set -e\necho um\necho dois"
	cfg, err := prepareWebProcess(types.ProcessConfig{
		Name:    "backup",
		Runtime: "auto",
		Script:  script + "\n",
		Cwd:     t.TempDir(),
	})
	if err != nil {
		t.Fatalf("prepareWebProcess: %v", err)
	}
	if cfg.Runtime != "shell" {
		t.Errorf("Runtime = %q: an inline script is a shell script", cfg.Runtime)
	}
	if len(cfg.Args) < 2 || cfg.Args[1] != script {
		t.Errorf("Args = %q, want the script passed with -c", cfg.Args)
	}
	if cfg.Launch == nil || cfg.Launch.Script != script {
		t.Errorf("Launch = %+v, want the script preserved for editing", cfg.Launch)
	}
}

func TestPrepareWebProcessHonoursTheChosenShell(t *testing.T) {
	cfg, err := prepareWebProcess(types.ProcessConfig{
		Name:        "com-sh",
		Runtime:     "bash",
		Entrypoint:  "uptime",
		Interpreter: "/bin/sh",
		Cwd:         t.TempDir(),
	})
	if err != nil {
		t.Fatalf("prepareWebProcess: %v", err)
	}
	if cfg.Entrypoint != "/bin/sh" {
		t.Errorf("Entrypoint = %q, want /bin/sh", cfg.Entrypoint)
	}
	if cfg.Launch == nil || cfg.Launch.Runtime != "shell" {
		t.Errorf("Launch = %+v, want the shell alias normalized", cfg.Launch)
	}
}

func TestPrepareWebProcessStillRejectsAnEmptyShellApplication(t *testing.T) {
	if _, err := prepareWebProcess(types.ProcessConfig{
		Name:    "vazio",
		Runtime: "shell",
		Cwd:     t.TempDir(),
	}); err == nil {
		t.Fatal("expected a shell application with neither command nor script to be rejected")
	}
}

// A command line is not a path. Translating it would rewrite words inside the
// command on installations that map host folders.
func TestPrepareProcessDoesNotTranslateAShellCommandLine(t *testing.T) {
	dir := t.TempDir()
	srv := setupTestServer(t)
	t.Cleanup(func() {
		if err := srv.supervisor.Shutdown(); err != nil {
			t.Error(err)
		}
	})
	cfg, err := srv.prepareProcess(types.ProcessConfig{
		Name:       "sem-traducao",
		Runtime:    "shell",
		Entrypoint: "ls -la /srv | head -3",
		Cwd:        dir,
	})
	if err != nil {
		t.Fatalf("prepareProcess: %v", err)
	}
	if cfg.Launch == nil || cfg.Launch.Entrypoint != "ls -la /srv | head -3" {
		t.Errorf("Launch = %+v, want the command line untouched", cfg.Launch)
	}
}
