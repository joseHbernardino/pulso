// Package envfile loads dotenv files without changing the daemon's environment.
package envfile

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/subosito/gotenv"
)

const MaxSize = 64 << 10

var ErrConflict = errors.New("o arquivo .env foi alterado; carregue a versão atual antes de salvar")
var writeMu sync.Mutex

type Document struct {
	Path     string `json:"path"`
	Content  string `json:"content"`
	Revision string `json:"revision"`
	Exists   bool   `json:"exists"`
}

// Resolve accepts dotenv filenames only. Relative paths are based on the application's cwd.
func Resolve(path, cwd string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", nil
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("caminho do .env: %w", err)
	}
	base := filepath.Base(path)
	if base != ".env" && !strings.HasPrefix(base, ".env.") && !strings.HasSuffix(base, ".env") {
		return "", errors.New("use um arquivo .env, .env.producao ou aplicacao.env")
	}
	return path, nil
}

func Parse(content string) (map[string]string, error) {
	if len(content) > MaxSize {
		return nil, errors.New("o arquivo .env deve ter no máximo 64 KiB")
	}
	if !utf8.ValidString(content) || strings.ContainsRune(content, 0) {
		return nil, errors.New("o arquivo .env deve conter texto UTF-8 sem bytes nulos")
	}
	values, err := gotenv.StrictParse(strings.NewReader(content))
	if err != nil {
		return nil, errors.New("sintaxe inválida no .env: use NOME=valor e feche as aspas dos valores")
	}
	for key, value := range values {
		if strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, 0) {
			return nil, errors.New("variável inválida no arquivo .env")
		}
	}
	return values, nil
}

func revision(content string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(content))) }

func Read(path string) (Document, error) {
	doc := Document{Path: path, Revision: "missing"}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return doc, fmt.Errorf("abrir pasta do .env: %w", err)
	}
	defer func() { _ = root.Close() }()
	return read(root, filepath.Base(path), doc)
}

func read(root *os.Root, name string, doc Document) (Document, error) {
	info, err := root.Lstat(name)
	if os.IsNotExist(err) {
		return doc, nil
	}
	if err != nil {
		return doc, fmt.Errorf("verificar .env: %w", err)
	}
	if !info.Mode().IsRegular() {
		return doc, errors.New("o .env deve ser um arquivo regular, sem link simbólico")
	}
	if info.Size() > MaxSize {
		return doc, errors.New("o arquivo .env deve ter no máximo 64 KiB")
	}
	f, err := root.OpenFile(name, os.O_RDONLY|safeReadFlags(), 0)
	if err != nil {
		return doc, fmt.Errorf("abrir .env: %w", err)
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil {
		return doc, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return doc, ErrConflict
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxSize+1))
	if err != nil {
		return doc, fmt.Errorf("ler .env: %w", err)
	}
	if len(data) > MaxSize || !utf8.Valid(data) || strings.ContainsRune(string(data), 0) {
		return doc, errors.New("o .env deve ser texto UTF-8 de até 64 KiB")
	}
	doc.Content, doc.Revision, doc.Exists = string(data), revision(string(data)), true
	return doc, nil
}

// Write validates first, checks the editor's revision, and atomically replaces the file.
// Existing permissions and ownership are preserved; new files are private (0600).
func Write(path, content, expected string) (Document, error) {
	if _, err := Parse(content); err != nil {
		return Document{}, err
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return Document{}, fmt.Errorf("abrir pasta do .env: %w", err)
	}
	defer func() { _ = root.Close() }()
	name := filepath.Base(path)
	old, err := read(root, name, Document{Path: path, Revision: "missing"})
	if err != nil {
		return Document{}, err
	}
	if expected == "" || old.Revision != expected {
		return Document{}, ErrConflict
	}
	if old.Content == content && old.Exists {
		return old, nil
	}
	mode := os.FileMode(0o600)
	var info os.FileInfo
	if old.Exists {
		info, err = root.Lstat(name)
		if err != nil {
			return Document{}, err
		}
		mode = info.Mode().Perm()
	}
	tmp := ".pulso-env-" + uuid.NewString()
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return Document{}, fmt.Errorf("criar arquivo temporário: %w", err)
	}
	defer func() { _ = root.Remove(tmp) }()
	defer func() { _ = f.Close() }()
	if _, err = f.WriteString(content); err != nil {
		return Document{}, err
	}
	if info != nil {
		if err = preserveOwner(f, info); err != nil {
			return Document{}, err
		}
	}
	if err = f.Chmod(mode); err != nil {
		return Document{}, err
	}
	if err = f.Sync(); err != nil {
		return Document{}, err
	}
	if err = f.Close(); err != nil {
		return Document{}, err
	}
	// Check again to catch changes made by other editors while the temporary file was written.
	current, err := read(root, name, Document{Path: path, Revision: "missing"})
	if err != nil {
		return Document{}, err
	}
	if current.Revision != expected {
		return Document{}, ErrConflict
	}
	if err = root.Rename(tmp, name); err != nil {
		return Document{}, fmt.Errorf("substituir .env: %w", err)
	}
	if runtime.GOOS == "windows" {
		return Document{Path: path, Content: content, Revision: revision(content), Exists: true}, nil
	}
	dir, err := root.Open(".")
	if err != nil {
		return Document{}, err
	}
	defer func() { _ = dir.Close() }()
	if err = dir.Sync(); err != nil {
		return Document{}, err
	}
	return Document{Path: path, Content: content, Revision: revision(content), Exists: true}, nil
}

// Environment merges file values and explicit overrides without modifying os.Environ.
func Environment(path, cwd string, overrides map[string]string) (map[string]string, string, error) {
	values := make(map[string]string)
	rev := ""
	if path != "" {
		resolved, err := Resolve(path, cwd)
		if err != nil {
			return nil, "", err
		}
		doc, err := Read(resolved)
		if err != nil {
			return nil, "", err
		}
		if !doc.Exists {
			return nil, "", errors.New("arquivo .env não encontrado")
		}
		values, err = Parse(doc.Content)
		if err != nil {
			return nil, "", err
		}
		rev = doc.Revision
	}
	for key, value := range overrides {
		values[key] = value
	}
	return values, rev, nil
}
