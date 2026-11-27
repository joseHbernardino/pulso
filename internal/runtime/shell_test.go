package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShellRuntimeNeverDetects(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "start.sh"), []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	shell := &ShellRuntime{}
	if shell.Detect(dir) {
		t.Fatal("the shell runtime must not be auto-detected: a project with a start.sh is not a shell project")
	}
	if _, err := NewDetector().Detect(dir); err == nil {
		t.Fatal("expected no runtime detected for a directory holding only a shell script")
	}
}

func TestDetectorResolvesShellAliases(t *testing.T) {
	detector := NewDetector()
	for _, name := range []string{"shell", "sh", "bash", "BASH", " zsh ", "script"} {
		got, err := detector.Get(name)
		if err != nil {
			t.Fatalf("Get(%q): %v", name, err)
		}
		if got.Name() != "shell" {
			t.Errorf("Get(%q) = %q, want shell", name, got.Name())
		}
	}
	if _, err := detector.Get("fortran"); err == nil {
		t.Error("expected an unknown runtime to be rejected")
	}
}

func TestShellCommandLineShapes(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "deploy.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "runner"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		opts StartOptions
		want []string
	}{
		{
			name: "command line is handed to the shell whole",
			opts: StartOptions{Entrypoint: "node server.js | tee app.log", Interpreter: "/bin/sh"},
			want: []string{"-c", "node server.js | tee app.log", "pulso"},
		},
		{
			name: "arguments are quoted onto the command line",
			opts: StartOptions{Entrypoint: "echo", Args: []string{"two words", "a'b"}, Interpreter: "/bin/sh"},
			want: []string{"-c", `echo 'two words' 'a'\''b'`, "pulso"},
		},
		{
			name: "inline script exposes positional parameters",
			opts: StartOptions{Script: "echo \"$1\"\n", Args: []string{"production"}, Interpreter: "/bin/sh"},
			want: []string{"-c", "echo \"$1\"\n", "pulso", "production"},
		},
		{
			name: "script is preferred over the entrypoint",
			opts: StartOptions{Script: "true", Entrypoint: "ignored", Interpreter: "/bin/sh"},
			want: []string{"-c", "true", "pulso"},
		},
		{
			name: "absolute script file is run as a file",
			opts: StartOptions{Entrypoint: scriptPath, Args: []string{"--fast"}, Interpreter: "/bin/sh"},
			want: []string{scriptPath, "--fast"},
		},
		{
			name: "relative existing file is run as a file",
			opts: StartOptions{Entrypoint: "./runner", Cwd: dir, Interpreter: "/bin/sh"},
			want: []string{"./runner"},
		},
		{
			name: "a .sh path that does not exist is still a script file",
			opts: StartOptions{Entrypoint: "/srv/missing.sh", Interpreter: "/bin/sh"},
			want: []string{"/srv/missing.sh"},
		},
		{
			name: "a bare word is a command, not a file",
			opts: StartOptions{Entrypoint: "uptime", Interpreter: "/bin/sh"},
			want: []string{"-c", "uptime", "pulso"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			shell, args, err := ShellCommandLine(test.opts)
			if err != nil {
				t.Fatalf("ShellCommandLine: %v", err)
			}
			if shell != "/bin/sh" {
				t.Errorf("shell = %q, want /bin/sh", shell)
			}
			if strings.Join(args, "\x00") != strings.Join(test.want, "\x00") {
				t.Errorf("args = %q, want %q", args, test.want)
			}
		})
	}
}

func TestShellCommandLineRequiresSomethingToRun(t *testing.T) {
	if _, _, err := ShellCommandLine(StartOptions{Interpreter: "/bin/sh"}); err == nil {
		t.Fatal("expected an error when neither a command nor a script was given")
	}
	if _, _, err := ShellCommandLine(StartOptions{Entrypoint: "   ", Script: "  \n ", Interpreter: "/bin/sh"}); err == nil {
		t.Fatal("expected blank input to be rejected")
	}
}

func TestShellBinaryPrefersExplicitThenEnvironment(t *testing.T) {
	if got := ShellBinary(" /usr/local/bin/fish "); got != "/usr/local/bin/fish" {
		t.Errorf("ShellBinary(explicit) = %q", got)
	}
	t.Setenv("PULSO_SHELL", "/bin/dash")
	if got := ShellBinary(""); got != "/bin/dash" {
		t.Errorf("ShellBinary(env) = %q, want /bin/dash", got)
	}
	t.Setenv("PULSO_SHELL", "")
	if got := ShellBinary(""); got == "" {
		t.Error("ShellBinary must always return a shell")
	}
}

func TestShellQuote(t *testing.T) {
	tests := map[string]string{
		"":             "''",
		"plain":        "plain",
		"two words":    "'two words'",
		"a'b":          `'a'\''b'`,
		"$HOME":        "'$HOME'",
		"rm -rf /; ok": "'rm -rf /; ok'",
	}
	for input, want := range tests {
		if got := ShellQuote(input); got != want {
			t.Errorf("ShellQuote(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestShellStartCmdCarriesEnvironmentAndDirectory(t *testing.T) {
	dir := t.TempDir()
	cmd, err := (&ShellRuntime{}).StartCmd(StartOptions{
		Entrypoint:  "env",
		Cwd:         dir,
		Env:         map[string]string{"PULSO_TEST_VALUE": "yes"},
		Interpreter: "/bin/sh",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Dir != dir {
		t.Errorf("cmd.Dir = %q, want %q", cmd.Dir, dir)
	}
	found := false
	for _, entry := range cmd.Env {
		if entry == "PULSO_TEST_VALUE=yes" {
			found = true
		}
	}
	if !found {
		t.Error("the overlay environment did not reach the command")
	}
}
