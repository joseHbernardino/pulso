package projects

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspect(t *testing.T) {
	for _, tc := range []struct {
		name      string
		files     map[string]string
		available map[string]bool
		want      string
		missing   string
	}{
		{"node-start", map[string]string{"package.json": `{"scripts":{"start":"node server.js"}}`}, map[string]bool{"npm": true}, "npm run start", ""},
		{"pnpm", map[string]string{"package.json": `{"packageManager":"pnpm@10.0.0","scripts":{"start":"node server.js"}}`}, nil, "pnpm run start", "pnpm"},
		{"python", map[string]string{"main.py": "raise RuntimeError('must not execute')"}, map[string]bool{"python3": true}, "main.py", ""},
		{"shell-missing-python311", map[string]string{"scripts/app.sh": "#!/bin/sh\nexec python3.11 -m app\n"}, map[string]bool{"sh": true}, "scripts/app.sh", "python3.11"},
		{"go", map[string]string{"go.mod": "module example.local/test\n"}, nil, "go run .", "go"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for path, content := range tc.files {
				if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, path)), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, path), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			report, err := Inspect(dir, func(command string) (string, error) {
				if tc.available[command] {
					return command, nil
				}
				return "", errors.New("not installed")
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(report.Suggestions) == 0 || !strings.Contains(report.Suggestions[0].Title, tc.want) {
				t.Fatalf("suggestions: %+v", report.Suggestions)
			}
			suggestion := report.Suggestions[0]
			if suggestion.Available != (tc.missing == "") {
				t.Fatalf("availability: %+v", suggestion)
			}
			if tc.missing != "" && (len(suggestion.Missing) != 1 || suggestion.Missing[0] != tc.missing) {
				t.Fatalf("missing: %v", suggestion.Missing)
			}
		})
	}
}

func TestInspectBoundsAndDoesNotReadSecretsOrSymlinks(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{".env": "TOKEN=secret\n", ".env.dev": "TOKEN=other-secret\n", ".env.example": "EXAMPLE=true\n", "package.json": strings.Repeat("x", maxManifest+1)} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(dir, ".env"), filepath.Join(dir, "app.py")); err != nil {
		t.Fatal(err)
	}
	report, err := Inspect(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.EnvFiles) != 2 || len(report.Suggestions) != 0 {
		t.Fatalf("unexpected discovery: %+v", report)
	}
	if strings.Contains(strings.Join(report.Notes, " "), "TOKEN") || !strings.Contains(strings.Join(report.Notes, " "), "128 KiB") {
		t.Fatalf("unsafe or missing diagnostics: %v", report.Notes)
	}
}
