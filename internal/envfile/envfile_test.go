package envfile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDotenvEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	content := "# keep this comment\nexport PULSO_ENV_TEST_A=from-file\nQUOTED=\"hello world # literal\"\nSINGLE='literal $HOME'\nEMPTY=\nMULTILINE=\"line one\nline two\"\n"
	doc, err := Write(path, content, "missing")
	if err != nil {
		t.Fatal(err)
	}
	values, rev, err := Environment(path, "", map[string]string{"PULSO_ENV_TEST_A": "override"})
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"PULSO_ENV_TEST_A": "override", "QUOTED": "hello world # literal", "SINGLE": "literal $HOME", "EMPTY": "", "MULTILINE": "line one\nline two"} {
		if values[key] != want {
			t.Errorf("%s = %q want %q", key, values[key], want)
		}
	}
	if rev != doc.Revision {
		t.Fatal("wrong revision")
	}
	if _, ok := os.LookupEnv("PULSO_ENV_TEST_A"); ok {
		t.Fatal("polluted daemon environment")
	}
	got, err := Read(path)
	if err != nil || got.Content != content {
		t.Fatalf("content not preserved: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("new file not private: %v", err)
	}
}

func TestWriteConflictsAndValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	original, err := Write(path, "TOKEN=original\n", "missing")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	updated, err := Write(path, "TOKEN=updated\n", original.Revision)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o640 {
		t.Fatal("permissions changed")
	}
	if _, err = Write(path, "TOKEN=stale\n", original.Revision); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict: %v", err)
	}
	for _, content := range []string{"invalid secret should not leak", "TOKEN=\"unclosed-secret", "TOKEN=\x00", strings.Repeat("X", MaxSize+1)} {
		if _, err = Write(path, content, updated.Revision); err == nil {
			t.Fatalf("accepted invalid content")
		}
		if strings.Contains(err.Error(), "unclosed-secret") {
			t.Fatal("secret leaked in error")
		}
	}
	got, _ := Read(path)
	if got.Content != updated.Content {
		t.Fatal("failed save changed the file")
	}
}

func TestFileBoundaries(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "actual.env")
	if err := os.WriteFile(target, []byte("TOKEN=secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".env")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil {
		t.Fatal("read symlink")
	}
	if _, err := Write(path, "TOKEN=new", "missing"); err == nil {
		t.Fatal("wrote symlink")
	}
	if _, err := Resolve("/etc/shadow", dir); err == nil {
		t.Fatal("accepted arbitrary file")
	}
	if got, err := Resolve(".env.production", dir); err != nil || got != filepath.Join(dir, ".env.production") {
		t.Fatalf("resolve: %s %v", got, err)
	}
	if _, _, err := Environment(filepath.Join(dir, "missing.env"), dir, nil); err == nil {
		t.Fatal("missing dotenv accepted")
	}
}
