package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pulso.local/pulso/pkg/types"
)

// A shell application declared in pulso.yaml is not resolved by the config
// loader, so the supervisor has to build the shell invocation itself.
func TestBuildArgsResolvesAnUnresolvedShellApplication(t *testing.T) {
	tests := []struct {
		name string
		cfg  types.ProcessConfig
		want []string
	}{
		{
			name: "command line",
			cfg:  types.ProcessConfig{Runtime: "shell", Entrypoint: "node app.js | tee log", Interpreter: "/bin/sh"},
			want: []string{"/bin/sh", "-c", "node app.js | tee log", "pulso"},
		},
		{
			name: "inline script without an entrypoint",
			cfg:  types.ProcessConfig{Runtime: "shell", Script: "echo \"$1\"", Args: []string{"producao"}, Interpreter: "/bin/sh"},
			want: []string{"/bin/sh", "-c", "echo \"$1\"", "pulso", "producao"},
		},
		{
			name: "a script implies the shell runtime",
			cfg:  types.ProcessConfig{Script: "uptime", Interpreter: "/bin/sh"},
			want: []string{"/bin/sh", "-c", "uptime", "pulso"},
		},
		{
			name: "a shell alias is accepted",
			cfg:  types.ProcessConfig{Runtime: "bash", Entrypoint: "uptime", Interpreter: "/bin/sh"},
			want: []string{"/bin/sh", "-c", "uptime", "pulso"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := buildArgs(test.cfg)
			if strings.Join(got, "\x00") != strings.Join(test.want, "\x00") {
				t.Errorf("buildArgs = %q, want %q", got, test.want)
			}
		})
	}
}

// An application the CLI or the panel already resolved carries a Launch
// record. Resolving it a second time would run the shell binary as a script.
func TestBuildArgsLeavesAResolvedShellApplicationAlone(t *testing.T) {
	cfg := types.ProcessConfig{
		Runtime:    "shell",
		Entrypoint: "/bin/sh",
		Args:       []string{"-c", "node app.js | tee log", "pulso"},
		Launch:     &types.LaunchConfig{Runtime: "shell", Entrypoint: "node app.js | tee log"},
	}
	got := buildArgs(cfg)
	want := []string{"/bin/sh", "-c", "node app.js | tee log", "pulso"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("buildArgs = %q, want %q", got, want)
	}
}

func TestBuildArgsUnchangedForOtherRuntimes(t *testing.T) {
	cfg := types.ProcessConfig{Runtime: "python", Interpreter: "/usr/bin/python3", Entrypoint: "worker.py", Args: []string{"--once"}}
	got := buildArgs(cfg)
	want := []string{"/usr/bin/python3", "worker.py", "--once"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("buildArgs = %q, want %q", got, want)
	}
}

// End to end: a configuration-shaped shell process really runs, and its
// output reaches the log file.
func TestShellProcessFromConfigRunsAndLogs(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sup := New(Options{LogDir: logDir, Defaults: types.DefaultsConfig{RestartPolicy: types.RestartNever}})
	defer func() {
		if err := sup.Shutdown(); err != nil {
			t.Error(err)
		}
	}()

	marker := filepath.Join(dir, "marker.txt")
	process, err := sup.AddProcess(context.Background(), types.ProcessConfig{
		Name:          "shell-config",
		Runtime:       "shell",
		Script:        "echo pronto | tee " + marker,
		Cwd:           dir,
		RestartPolicy: types.RestartNever,
	})
	if err != nil {
		t.Fatalf("AddProcess: %v", err)
	}

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if content, err := os.ReadFile(marker); err == nil && strings.Contains(string(content), "pronto") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	info := process.Info()
	t.Fatalf("the shell script never ran: state %s, exit %d, last reason %q", info.State, info.ExitCode, info.LastReason)
}
