package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDotenvConfigInheritanceAndDiff(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pulso.yaml")
	data := "processes:\n  - name: base\n    entrypoint: sleep\n    env_file: .env.shared\n  - name: inherited\n    extends: base\n  - name: custom\n    extends: base\n    env_file: custom.env\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Processes[1].EnvFile != ".env.shared" || cfg.Processes[2].EnvFile != "custom.env" || cfg.Processes[1].Cwd != dir {
		t.Fatal("dotenv paths not loaded or inherited correctly")
	}
	before := cfg.Processes[2]
	cfg.Processes[2].EnvFile = ".env.changed"
	changed := diffProcess(before, cfg.Processes[2])
	if len(changed) != 1 || changed[0] != "env_file" {
		t.Fatalf("dotenv change not detected: %v", changed)
	}
}
