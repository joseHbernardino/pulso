package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"pulso.local/pulso/pkg/types"
)

func TestShellProcessName(t *testing.T) {
	tests := []struct {
		name       string
		script     string
		entrypoint string
		want       string
	}{
		{"command line", "", "npm run start", "npm"},
		{"script file", "", "/srv/api/start.sh", "start.sh"},
		{"skips the shebang and keywords", "#!/usr/bin/env bash\nset -euo pipefail\npg_dump app | gzip", "", "pg_dump"},
		{"skips an assignment", "", "ENV=production rails server", "rails"},
		{"keeps a path basename", "", "/usr/local/bin/backup-now --all", "backup-now"},
		{"falls back to a noise word", "echo pronto", "", "echo"},
		{"falls back to shell", "   \n# apenas um comentário\n", "", "shell"},
		{"strips unusable characters", "", "(subshell)", "subshell"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shellProcessName(test.script, test.entrypoint); got != test.want {
				t.Errorf("shellProcessName(%q, %q) = %q, want %q", test.script, test.entrypoint, got, test.want)
			}
		})
	}
}

func TestSanitizeProcessName(t *testing.T) {
	tests := map[string]string{
		"api":        "api",
		"my-app.sh":  "my-app.sh",
		"-leading":   "leading",
		"./relative": "relative",
		"|":          "",
		"":           "",
	}
	for input, want := range tests {
		if got := sanitizeProcessName(input); got != want {
			t.Errorf("sanitizeProcessName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestResolveExecScript(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "deploy.sh")
	if err := os.WriteFile(scriptPath, []byte("echo implantando\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	emptyPath := filepath.Join(dir, "empty.sh")
	if err := os.WriteFile(emptyPath, []byte("   \n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("arguments", func(t *testing.T) {
		script, args, err := resolveExecScript(&cobra.Command{}, "", []string{"echo $1", "producao"})
		if err != nil {
			t.Fatal(err)
		}
		if script != "echo $1" {
			t.Errorf("script = %q", script)
		}
		if len(args) != 1 || args[0] != "producao" {
			t.Errorf("args = %q, want [producao]", args)
		}
	})

	t.Run("file", func(t *testing.T) {
		script, args, err := resolveExecScript(&cobra.Command{}, scriptPath, []string{"extra"})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(script, "implantando") {
			t.Errorf("script = %q", script)
		}
		if len(args) != 1 || args[0] != "extra" {
			t.Errorf("args = %q: every argument is positional when the script comes from a file", args)
		}
	})

	t.Run("standard input", func(t *testing.T) {
		cmd := &cobra.Command{}
		cmd.SetIn(strings.NewReader("echo da entrada\n"))
		script, _, err := resolveExecScript(cmd, "-", nil)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(script, "da entrada") {
			t.Errorf("script = %q", script)
		}
	})

	t.Run("refusals", func(t *testing.T) {
		if _, _, err := resolveExecScript(&cobra.Command{}, "", nil); err == nil {
			t.Error("expected an error when no command was given")
		}
		if _, _, err := resolveExecScript(&cobra.Command{}, emptyPath, nil); err == nil {
			t.Error("expected an empty script file to be rejected")
		}
		if _, _, err := resolveExecScript(&cobra.Command{}, filepath.Join(dir, "missing.sh"), nil); err == nil {
			t.Error("expected a missing script file to be rejected")
		}
		blank := &cobra.Command{}
		blank.SetIn(strings.NewReader("\n  \n"))
		if _, _, err := resolveExecScript(blank, "-", nil); err == nil {
			t.Error("expected blank standard input to be rejected")
		}
	})
}

func TestShellSourceLine(t *testing.T) {
	tests := []struct {
		name string
		cfg  types.ProcessConfig
		want string
	}{
		{
			name: "inline script reports its first real line and the rest",
			cfg:  types.ProcessConfig{Script: "#!/bin/bash\nset -e\necho um\necho dois"},
			want: "set -e (+2 more lines)",
		},
		{
			name: "single line script",
			cfg:  types.ProcessConfig{Script: "uptime"},
			want: "uptime",
		},
		{
			name: "a resolved command line is read from the launch record",
			cfg: types.ProcessConfig{
				Runtime:    "shell",
				Entrypoint: "/bin/sh",
				Args:       []string{"-c", "node app.js | tee log", "pulso"},
				Launch:     &types.LaunchConfig{Runtime: "shell", Entrypoint: "node app.js | tee log"},
			},
			want: "node app.js | tee log",
		},
		{
			name: "other runtimes have no shell source",
			cfg:  types.ProcessConfig{Runtime: "python", Entrypoint: "worker.py", Launch: &types.LaunchConfig{Runtime: "python", Entrypoint: "worker.py"}},
			want: "",
		},
		{
			name: "nothing to report",
			cfg:  types.ProcessConfig{Runtime: "shell", Script: "\n# apenas comentário\n"},
			want: "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shellSourceLine(test.cfg); got != test.want {
				t.Errorf("shellSourceLine = %q, want %q", got, test.want)
			}
		})
	}

	long := shellSourceLine(types.ProcessConfig{Script: strings.Repeat("x", 300)})
	if len([]rune(long)) != 161 {
		t.Errorf("a long line was kept at %d runes, want 160 plus the ellipsis", len([]rune(long)))
	}
}
