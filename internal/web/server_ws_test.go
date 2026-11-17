package web

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"pulso.local/pulso/pkg/types"
)

func TestProcessListMessageOmitsEmbeddedConfig(t *testing.T) {
	startedAt := time.Unix(1_700_000_000, 0).UTC()
	procs := []types.ProcessInfo{
		{
			ID:         "12345678-1234-1234-1234-123456789abc",
			NumericID:  7,
			Name:       "api",
			Runtime:    "go",
			State:      types.StateRunning,
			PID:        4242,
			Restarts:   3,
			StartedAt:  &startedAt,
			CPUPercent: 12.5,
			MemBytes:   64 * 1024 * 1024,
			Config: types.ProcessConfig{
				Name:       "api",
				Entrypoint: "./server",
				Env: map[string]string{
					"SECRET": "should-not-be-broadcast",
				},
			},
		},
	}

	msg := ProcessListMessage{
		Type:      "process_list",
		Processes: summarizeProcesses(procs),
		Timestamp: time.Now().Unix(),
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}

	got := string(data)
	if strings.Contains(got, "should-not-be-broadcast") {
		t.Fatalf("websocket payload leaked process config: %s", got)
	}
	if strings.Contains(got, "\"config\"") {
		t.Fatalf("websocket payload should not include embedded config: %s", got)
	}
	if !strings.Contains(got, "\"cpu_percent\":12.5") {
		t.Fatalf("websocket payload missing cpu metric: %s", got)
	}
	if !strings.Contains(got, "\"memory_bytes\":67108864") {
		t.Fatalf("websocket payload missing memory metric: %s", got)
	}
}

func TestProcessListMessagePreservesDiagnostics(t *testing.T) {
	now := time.Now().UTC()
	code := 127
	result := summarizeProcesses([]types.ProcessInfo{{
		ID: "failed", State: types.StateCrashed, ExitCode: code, LastEvent: "restart_scheduled", LastReason: "retrying", FinishedAt: &now,
		LastFailure: &types.ProcessFailure{Phase: "exit", Message: "exit 127", OccurredAt: now, ExitCode: &code}, PendingChanges: true,
	}})
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var restored []types.ProcessInfo
	if err = json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	p := restored[0]
	if p.LastFailure == nil || *p.LastFailure.ExitCode != code || p.LastEvent != "restart_scheduled" || p.LastReason != "retrying" || p.FinishedAt == nil || !p.PendingChanges {
		t.Fatalf("live update lost diagnostic information: %s", data)
	}
}
