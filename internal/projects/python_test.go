package projects

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPythonDependencyDetection(t *testing.T) {
	for _, tc := range []struct {
		name    string
		files   map[string]string
		manager string
		error   string
	}{
		{"requirements", map[string]string{"requirements.txt": "anyio==4.11.0"}, "pip / requirements.txt", ""},
		{"poetry-lock-over-export", map[string]string{"pyproject.toml": "[project]\nname='app'", "poetry.lock": "locked", "requirements.txt": "stale"}, "Poetry", ""},
		{"poetry-without-lock", map[string]string{"pyproject.toml": "[tool.poetry]\npackage-mode=false\n[tool.poetry.dependencies]\nanyio='^4'"}, "Poetry", ""},
		{"poetry-modern", map[string]string{"pyproject.toml": "[project]\nname='app'\n[build-system]\nbuild-backend='poetry.core.masonry.api'"}, "Poetry", ""},
		{"quoted-table", map[string]string{"pyproject.toml": "[\"tool\".\"poetry\"]\npackage-mode=false"}, "Poetry", ""},
		{"pep621-pip", map[string]string{"pyproject.toml": "[project]\nname='app'\ndependencies=['anyio']"}, "pip / pyproject.toml", ""},
		{"comment-not-poetry", map[string]string{"pyproject.toml": "# [tool.poetry]\n[project]\nname='app'"}, "pip / pyproject.toml", ""},
		{"stdlib", nil, "biblioteca padrão", ""},
		{"orphan-lock", map[string]string{"poetry.lock": "locked", "requirements.txt": "anyio"}, "", "sem pyproject.toml"},
		{"invalid-toml", map[string]string{"pyproject.toml": "[project"}, "", "inválido"},
		{"bounded-manifest", map[string]string{"pyproject.toml": strings.Repeat("x", maxManifest+1)}, "", "128 KiB"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, data := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			plan, err := pythonDependencyPlan(dir)
			if tc.error != "" {
				if err == nil || !strings.Contains(err.Error(), tc.error) {
					t.Fatalf("expected %s, got %v", tc.error, err)
				}
				return
			}
			if err != nil || plan.manager != tc.manager {
				t.Fatalf("manager=%s err=%v", plan.manager, err)
			}
		})
	}
}

func TestPythonProjectRootAndExcludedDependencies(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte("[tool.poetry]\npackage-mode=false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	scripts := filepath.Join(dir, "scripts")
	if err := os.Mkdir(scripts, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := pythonDependencyPlan(scripts); err == nil || !strings.Contains(err.Error(), "pasta acima") {
		t.Fatalf("missing project-root diagnostic: %v", err)
	}
	for _, name := range []string{"main.py", "poetry.lock", ".dockerignore"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("poetry.lock\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Archive(context.Background(), dir, "python", "3.11"); err == nil || !strings.Contains(err.Error(), "poetry.lock ficou fora") {
		t.Fatalf("ignored lockfile was silently lost: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".dockerignore"), []byte("pyproject.toml\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Archive(context.Background(), dir, "python", "3.11"); err == nil || !strings.Contains(err.Error(), "pyproject.toml ficou fora") {
		t.Fatalf("ignored manifest was silently lost: %v", err)
	}
}
