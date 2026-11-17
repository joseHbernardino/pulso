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

func awaitDiagnostic(t *testing.T, p *ManagedProcess, predicate func(types.ProcessInfo) bool) types.ProcessInfo {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		info := p.Info()
		if predicate(info) {
			return info
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("unexpected process result: %+v", p.Info())
	return types.ProcessInfo{}
}

func TestFailedProcessDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, script, message string
		code                  int
	}{
		{"stderr-without-newline", "printf 'python3.11: command not found' >&2; exit 127", "python3.11: command not found", 127},
		{"silent", "exit 42", "", 42},
		{"signal", "kill -TERM $$", "", -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New(Options{LogDir: t.TempDir()})
			t.Cleanup(func() { _ = s.Shutdown() })
			p, err := s.AddProcess(context.Background(), types.ProcessConfig{Name: tc.name, Entrypoint: "sh", Args: []string{"-c", tc.script}, RestartPolicy: types.RestartNever})
			if err != nil {
				t.Fatal(err)
			}
			info := awaitDiagnostic(t, p, func(i types.ProcessInfo) bool { return i.LastFailure != nil })
			if info.ExitCode != tc.code || info.LastFailure.ExitCode == nil || *info.LastFailure.ExitCode != tc.code || info.PID != 0 || info.FinishedAt == nil {
				t.Fatalf("incorrect exit diagnostics: %+v", info)
			}
			if tc.code == -1 && !strings.Contains(info.LastFailure.Message, "signal") {
				t.Fatalf("signal missing: %+v", info.LastFailure)
			}
			<-p.exited
			data, err := os.ReadFile(s.LogPathStderr(p.ID))
			if err != nil || !strings.Contains(string(data), tc.message) {
				t.Fatalf("stderr: %q %v", data, err)
			}
			time.Sleep(20 * time.Millisecond)
			if p.Info().Uptime != info.Uptime {
				t.Fatal("uptime keeps growing after exit")
			}
			*info.LastFailure.ExitCode = 999
			if *p.Info().LastFailure.ExitCode != tc.code {
				t.Fatal("failure exposes mutable internal state")
			}
		})
	}
}

func TestAutomaticRestartSurvivesCallback(t *testing.T) {
	dir := t.TempDir()
	s := New(Options{LogDir: dir, Defaults: types.DefaultsConfig{BackoffBase: 10 * time.Millisecond, BackoffMax: 20 * time.Millisecond}})
	t.Cleanup(func() { _ = s.Shutdown() })
	script := "if [ ! -f marker ]; then touch marker; exit 7; fi; exec sleep 60"
	p, err := s.AddProcess(context.Background(), types.ProcessConfig{Name: "restart-once", Entrypoint: "sh", Args: []string{"-c", script}, Cwd: dir, RestartPolicy: types.RestartOnFailure, MinUptime: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	info := awaitDiagnostic(t, p, func(i types.ProcessInfo) bool {
		return i.State == types.StateRunning && i.Restarts == 1 && i.LastEvent == "restarted"
	})
	time.Sleep(300 * time.Millisecond)
	after := p.Info()
	if after.State != types.StateRunning || after.PID != info.PID || after.Restarts != 1 {
		t.Fatalf("automatic restart did not survive callback: %+v", after)
	}
	if after.LastFailure == nil || *after.LastFailure.ExitCode != 7 {
		t.Fatalf("previous failure lost: %+v", after.LastFailure)
	}
}

func TestStartFailureAndRecoverySnapshotKeepCause(t *testing.T) {
	dir := t.TempDir()
	s := New(Options{LogDir: dir})
	t.Cleanup(func() { _ = s.Shutdown() })
	executable := filepath.Join(dir, "app.sh")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexec sleep 60\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	p, err := s.AddProcess(context.Background(), types.ProcessConfig{Name: "removed-command", Entrypoint: executable, RestartPolicy: types.RestartNever})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(executable); err != nil {
		t.Fatal(err)
	}
	if err = s.RestartProcess(context.Background(), p.ID); err == nil {
		t.Fatal("missing command accepted")
	}
	failure := p.Info().LastFailure
	if failure == nil || failure.Phase != "start" || !strings.Contains(failure.Message, "no such file") {
		t.Fatalf("cause missing: %+v", failure)
	}
	if err = s.StopProcess(p.ID, false, time.Second); err != nil {
		t.Fatal(err)
	}
	if err = s.Save(); err != nil {
		t.Fatal(err)
	}
	restored := New(Options{LogDir: dir})
	t.Cleanup(func() { _ = restored.Shutdown() })
	if err = restored.Resurrect(); err != nil {
		t.Fatal(err)
	}
	other, err := restored.Get(p.Config.Name)
	if err != nil {
		t.Fatal(err)
	}
	if other.Info().LastFailure == nil || other.Info().LastFailure.Message != failure.Message {
		t.Fatal("saved cause lost on recovery")
	}
}
