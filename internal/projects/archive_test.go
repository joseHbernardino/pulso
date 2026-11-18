package projects

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveExcludesHostSecretsAndGeneratesPythonEnvironment(t *testing.T) {
	dir := t.TempDir()
	for name, value := range map[string]string{"main.py": "print('hello')", ".env": "SECRET=private", ".env.dev": "SECRET=other"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, ".venv"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".venv", "ignore"), []byte("host-binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	encoded, err := Archive(context.Background(), dir, "python", "3.11")
	if err != nil {
		t.Fatal(err)
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gz.Close() }()
	reader := tar.NewReader(gz)
	files := map[string]string{}
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		files[header.Name] = string(content)
	}
	if len(files) != 2 || files["main.py"] == "" || !strings.Contains(files[".pulso.Dockerfile"], "FROM python:3.11-slim-bookworm") || !strings.Contains(files[".pulso.Dockerfile"], "USER appuser") {
		t.Fatalf("unsafe archive: %v", files)
	}
	if _, err := Archive(context.Background(), dir, "python", "3.11\nRUN injected"); err == nil {
		t.Fatal("Dockerfile injection accepted")
	}
}

func TestArchiveRejectsExternalSymlinks(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.py"), []byte("pass"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(dir, "outside")); err != nil {
		t.Fatal(err)
	}
	if _, err := Archive(context.Background(), dir, "python", "3.11"); err == nil {
		t.Fatal("external file accepted")
	}
}

func TestArchiveAppliesDockerignoreBeforeSizeLimitAndHonorsExceptions(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "logs"), 0o750); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"main.py": "pass", ".dockerignore": "logs\n!logs/keep.txt\n", "logs/keep.txt": "keep"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	file, err := os.Create(filepath.Join(dir, "logs", "huge.log"))
	if err != nil {
		t.Fatal(err)
	}
	if err = file.Truncate(128 << 20); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	encoded, err := Archive(context.Background(), dir, "python", "3.11")
	if err != nil {
		t.Fatal("ignored files were counted against the context limit:", err)
	}
	data, _ := base64.StdEncoding.DecodeString(encoded)
	gz, err := gzip.NewReader(strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gz.Close() }()
	tarReader := tar.NewReader(gz)
	found := map[string]bool{}
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		found[header.Name] = true
	}
	if !found["logs/keep.txt"] || found["logs/huge.log"] {
		t.Fatal("incorrect ignore/exclusion behavior:", found)
	}
}

func TestCustomPythonCommandIsNotBakedIntoImage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "worker.py"), []byte("pass"), 0o600); err != nil {
		t.Fatal(err)
	}
	encoded, err := Archive(context.Background(), dir, "python", "3.11", "python", "worker.py", "--key", "private-argument")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := base64.StdEncoding.DecodeString(encoded)
	gz, err := gzip.NewReader(strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gz.Close() }()
	tarReader := tar.NewReader(gz)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Name == ".pulso.Dockerfile" {
			content, err := io.ReadAll(tarReader)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(content), "private-argument") {
				t.Fatal("runtime arguments included in image recipe")
			}
		}
	}
}
