package main

import (
	"reflect"
	"testing"

	"pulso.local/pulso/pkg/types"
)

func TestRunDeployExecutesExpectedCommands(t *testing.T) {
	cfg := &types.PulsoConfig{
		Deploy: map[string]types.DeployTarget{
			"prod": {
				Host:          "example.com",
				User:          "deploy",
				Port:          2222,
				Path:          "/srv/pulso",
				PreDeploy:     "echo pre",
				PostDeploy:    "echo post",
				ReloadCommand: "pulso --config pulso.yaml daemon reload",
			},
		},
	}

	var calls [][]string
	runner := func(name string, args ...string) error {
		call := append([]string{name}, args...)
		calls = append(calls, call)
		return nil
	}

	err := runDeploy("prod", "/tmp/prod.pulso.toml", cfg, runner)
	if err != nil {
		t.Fatalf("runDeploy returned error: %v", err)
	}

	want := [][]string{
		{"sh", "-c", "echo pre"},
		{"ssh", "-p", "2222", "deploy@example.com", "mkdir -p '/srv/pulso'"},
		{"scp", "-P", "2222", "/tmp/prod.pulso.toml", "deploy@example.com:/srv/pulso/prod.pulso.toml"},
		{"ssh", "-p", "2222", "deploy@example.com", "cd '/srv/pulso' && echo post"},
		{"ssh", "-p", "2222", "deploy@example.com", "cd '/srv/pulso' && pulso --config pulso.yaml daemon reload"},
	}

	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("unexpected calls:\n got: %#v\nwant: %#v", calls, want)
	}
}

func TestRunDeployMissingTarget(t *testing.T) {
	err := runDeploy("prod", "/tmp/pulso.yaml", &types.PulsoConfig{}, func(name string, args ...string) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected missing target error")
	}
}
