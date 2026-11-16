package supervisor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"pulso.local/pulso/pkg/types"
	"testing"
	"time"
)

func TestAutomaticRecoveryPreservesDesiredState(t *testing.T) {
	dir := t.TempDir()
	s := New(Options{LogDir: dir, AutoSave: true})
	t.Cleanup(func() { _ = s.Shutdown() })
	var pausedID string
	for _, name := range []string{"active", "0", "removed"} {
		proc, err := s.AddProcess(context.Background(), types.ProcessConfig{Name: name, Entrypoint: "sleep", Args: []string{"60"}, RestartPolicy: types.RestartNever})
		if err != nil {
			t.Fatal(err)
		}
		if name == "0" {
			pausedID = proc.ID
		}
	}
	if err := s.StopProcess(pausedID, false, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveProcess("removed"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "dump.json"))
	if err != nil {
		t.Fatal(err)
	}
	var snapshots []processSnapshot
	if err = json.Unmarshal(before, &snapshots); err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 2 {
		t.Fatalf("snapshots=%d", len(snapshots))
	}
	for _, snap := range snapshots {
		if snap.RunOnRestore == nil || *snap.RunOnRestore != (snap.Config.Name == "active") {
			t.Fatalf("wrong desired state for %s", snap.Config.Name)
		}
	}
	fi, err := os.Stat(filepath.Join(dir, "dump.json"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("private snapshot permissions: %v", err)
	}
	if err = s.Shutdown(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(dir, "dump.json"))
	if err != nil || string(after) != string(before) {
		t.Fatal("shutdown overwrote the recovery snapshot")
	}
	recovered := New(Options{LogDir: dir, AutoSave: true})
	t.Cleanup(func() { _ = recovered.Shutdown() })
	if err = recovered.Resurrect(); err != nil {
		t.Fatal(err)
	}
	if err = recovered.Resurrect(); err != nil {
		t.Fatal("restore is not idempotent:", err)
	}
	if len(recovered.List()) != 2 {
		t.Fatal("unexpected restored process count")
	}
	active, _ := recovered.Get("active")
	recovered.mu.RLock()
	paused := recovered.procs[recovered.byName["0"]]
	recovered.mu.RUnlock()
	if paused == nil {
		t.Fatal("numeric application name was not restored")
	}
	if active.GetState() != types.StateRunning || paused.GetState() != types.StateStopped || paused.Info().PID != 0 {
		t.Fatal("running/stopped state was not restored")
	}
	for _, snap := range snapshots {
		recovered.mu.RLock()
		p := recovered.procs[recovered.byName[snap.Config.Name]]
		recovered.mu.RUnlock()
		if p.NumericID != snap.NumericID {
			t.Fatal("numeric identifier changed")
		}
	}
	if err = recovered.RestartProcess(context.Background(), paused.ID); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "dump.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &snapshots); err != nil {
		t.Fatal(err)
	}
	for _, snap := range snapshots {
		if snap.RunOnRestore == nil || !*snap.RunOnRestore {
			t.Fatal("restart was not persisted")
		}
	}
}

func TestRecoveryDoesNotSilentlyDiscardInvalidSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dump.json")
	if err := os.WriteFile(path, []byte("invalid json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := New(Options{LogDir: dir, AutoSave: true})
	defer func() { _ = s.Shutdown() }()
	if err := s.Resurrect(); err == nil {
		t.Fatal("corrupt snapshot accepted")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "invalid json" {
		t.Fatal("invalid snapshot overwritten")
	}
}
