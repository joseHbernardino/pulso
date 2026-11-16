package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"pulso.local/pulso/internal/envfile"
	"pulso.local/pulso/pkg/types"
)

func waitFileValue(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(path)
		if string(data) == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	data, _ := os.ReadFile(path)
	t.Fatalf("file = %q, want %q", data, want)
}

func TestSettingsApplyOnExplicitRestart(t *testing.T) {
	dir := t.TempDir()
	envPath, output := filepath.Join(dir, ".env"), filepath.Join(dir, "observed")
	doc, err := envfile.Write(envPath, "PULSO_ENV_STAGE=first\nPULSO_ENV_REMOVED=present\n", "missing")
	if err != nil {
		t.Fatal(err)
	}
	sup := New(Options{LogDir: dir, AutoSave: true, Defaults: types.DefaultsConfig{RestartPolicy: types.RestartNever}})
	defer func() { _ = sup.Shutdown() }()
	cfg := types.ProcessConfig{Name: "env-app", Runtime: "process", Entrypoint: "sh", Args: []string{"-c", `printf '%s|%s|%s' "$PULSO_ENV_STAGE" "$PULSO_ENV_REMOVED" "$EXPLICIT" > "$1"; exec sleep 60`, "sh", output}, Cwd: dir, EnvFile: envPath, Env: map[string]string{"EXPLICIT": "one"}, RestartPolicy: types.RestartNever}
	proc, err := sup.AddProcess(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	waitFileValue(t, output, "first|present|one")
	pid, id, numericID := proc.Info().PID, proc.ID, proc.NumericID
	// Exercise dashboard snapshots while settings are staged and replaced.
	var readers sync.WaitGroup
	done := make(chan struct{})
	readers.Go(func() {
		for {
			select {
			case <-done:
				return
			default:
				sup.List()
			}
		}
	})
	defer func() { close(done); readers.Wait() }()
	desired, revision, err := sup.ProcessSettings(id)
	if err != nil {
		t.Fatal(err)
	}
	desired.Env["EXPLICIT"] = "two"
	content := "PULSO_ENV_STAGE=second\n"
	if err = sup.UpdateSettings(id, desired, revision, &content, doc.Revision); err != nil {
		t.Fatal(err)
	}
	if info := proc.Info(); info.PID != pid || !info.PendingChanges || info.Config.Env["EXPLICIT"] != "one" {
		t.Fatal("save altered active process")
	}
	waitFileValue(t, output, "first|present|one")
	dump, err := os.ReadFile(filepath.Join(dir, "dump.json"))
	if err != nil {
		t.Fatal(err)
	}
	var snapshots []processSnapshot
	if err := json.Unmarshal(dump, &snapshots); err != nil {
		t.Fatal(err)
	}
	if _, leaked := snapshots[0].Config.Env["PULSO_ENV_STAGE"]; leaked {
		t.Fatal("resolved file secrets persisted in snapshot")
	}
	if !strings.Contains(string(dump), `"EXPLICIT": "two"`) {
		t.Fatal("desired config not saved")
	}
	if err = sup.RestartProcess(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	waitFileValue(t, output, "second||two")
	current, _ := sup.Get(id)
	if info := current.Info(); info.ID != id || info.NumericID != numericID || info.PID == pid || info.PendingChanges {
		t.Fatal("restart lost identity or pending status")
	}
	if _, ok := os.LookupEnv("PULSO_ENV_STAGE"); ok {
		t.Fatal("environment leaked to daemon")
	}
	// Preflight must keep the working process alive when the file becomes invalid.
	if err = os.WriteFile(envPath, []byte("TOKEN=\"unclosed"), 0o600); err != nil {
		t.Fatal(err)
	}
	pid = current.Info().PID
	if err = sup.RestartProcess(context.Background(), id); err == nil {
		t.Fatal("invalid dotenv restarted app")
	}
	if current.Info().PID != pid || current.GetState() != types.StateRunning {
		t.Fatal("preflight stopped working process")
	}
}

func TestSettingsConflictSharedFileAndRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	doc, err := envfile.Write(path, "APP=first\n", "missing")
	if err != nil {
		t.Fatal(err)
	}
	sup := New(Options{LogDir: dir, AutoSave: true})
	defer func() { _ = sup.Shutdown() }()
	for _, name := range []string{"first", "second"} {
		if _, err = sup.AddProcess(context.Background(), types.ProcessConfig{Name: name, Entrypoint: "sleep", Args: []string{"60"}, Cwd: dir, EnvFile: path, RestartPolicy: types.RestartNever}); err != nil {
			t.Fatal(err)
		}
	}
	cfg, revision, _ := sup.ProcessSettings("first")
	content := "APP=next\n"
	cfg.Args = []string{"120"}
	if err = sup.UpdateSettings("first", cfg, revision, &content, doc.Revision); err != nil {
		t.Fatal(err)
	}
	if err = sup.UpdateSettings("first", cfg, revision, nil, ""); !errors.Is(err, ErrConfigConflict) {
		t.Fatalf("expected stale config conflict: %v", err)
	}
	second, _ := sup.Get("second")
	if !second.Info().PendingChanges {
		t.Fatal("shared file change not reflected")
	}
	if err = sup.StopProcess("first", false, time.Second); err != nil {
		t.Fatal(err)
	}
	if err = sup.StopProcess("second", false, time.Second); err != nil {
		t.Fatal(err)
	}
	if err = sup.Shutdown(); err != nil {
		t.Fatal(err)
	}
	restored := New(Options{LogDir: dir})
	defer func() { _ = restored.Shutdown() }()
	if err = restored.Resurrect(); err != nil {
		t.Fatal(err)
	}
	proc, err := restored.Get("first")
	if err != nil || proc.Config.Args[0] != "120" || proc.Config.EnvFile != path || proc.GetState() != types.StateStopped {
		t.Fatalf("desired configuration not restored: %v", err)
	}
}

func TestSettingsUnchangedDoesNotRequireRestart(t *testing.T) {
	sup := New(Options{})
	defer func() { _ = sup.Shutdown() }()
	proc, err := sup.AddProcess(context.Background(), types.ProcessConfig{Name: "unchanged", Entrypoint: "sleep", Args: []string{"60"}, Cwd: t.TempDir(), Env: map[string]string{}, RestartPolicy: types.RestartNever})
	if err != nil {
		t.Fatal(err)
	}
	cfg, revision, err := sup.ProcessSettings(proc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = sup.UpdateSettings(proc.ID, cfg, revision, nil, ""); err != nil {
		t.Fatal(err)
	}
	if proc.Info().PendingChanges {
		t.Fatal("unchanged settings created pending restart")
	}
}
