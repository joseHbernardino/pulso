package projects

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/moby/patternmatcher"
	"github.com/moby/patternmatcher/ignorefile"
)

// Archive packages a project for Docker, excluding secrets, host virtual
// environments and dependency caches. Docker applies .dockerignore at build time.
func Archive(ctx context.Context, root, kind, version string, customCommand ...string) (string, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) == "/" {
		return "", fmt.Errorf("selecione uma pasta específica do projeto")
	}
	var dockerfile string
	var requiredFiles []string
	var err error
	if kind == "dockerfile" {
		if _, err = readManifest(filepath.Join(root, "Dockerfile")); err != nil {
			return "", fmt.Errorf("arquivo Dockerfile: %w", err)
		}
	} else {
		dockerfile, requiredFiles, err = projectDockerfile(root, kind, version, len(customCommand) > 0)
		if err != nil {
			return "", err
		}
	}
	var buffer bytes.Buffer
	var patterns []string
	ignorePath := filepath.Join(root, ".dockerignore")
	if _, err := os.Lstat(ignorePath); err == nil {
		data, err := readManifest(ignorePath)
		if err != nil {
			return "", fmt.Errorf("arquivo .dockerignore: %w", err)
		}
		patterns, err = ignorefile.ReadAll(bytes.NewReader(data))
		if err != nil {
			return "", fmt.Errorf("arquivo .dockerignore: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	matcher, err := patternmatcher.New(patterns)
	if err != nil {
		return "", fmt.Errorf("padrões do .dockerignore: %w", err)
	}
	gz := gzip.NewWriter(&buffer)
	tw := tar.NewWriter(gz)
	var total int64
	count := 0
	included := make(map[string]bool)
	secured, err := os.OpenRoot(root)
	if err != nil {
		return "", err
	}
	defer func() { _ = secured.Close() }()
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		name := entry.Name()
		if name == ".git" || name == "node_modules" || name == ".venv" || name == "venv" || name == "__pycache__" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if name == ".env" || strings.HasPrefix(name, ".env.") || strings.HasSuffix(name, ".env") || name == ".pulso.Dockerfile" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if rel != "Dockerfile" && rel != ".dockerignore" {
			ignored, err := matcher.MatchesOrParentMatches(rel)
			if err != nil {
				return err
			}
			if ignored {
				if entry.IsDir() && !matcher.Exclusions() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		count++
		if count > 20000 {
			return fmt.Errorf("o projeto excede 20000 arquivos; use uma pasta menor ou uma imagem publicada")
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() && !entry.IsDir() {
			return fmt.Errorf("o contexto contém um link ou arquivo especial: %s; use arquivos regulares dentro do projeto", rel)
		}
		if entry.IsDir() {
			return nil
		}
		total += info.Size()
		if total > 64<<20 {
			return fmt.Errorf("o contexto excede 64 MiB; use uma pasta menor ou uma imagem publicada")
		}
		file, err := secured.Open(rel)
		if err != nil {
			return err
		}
		opened, err := file.Stat()
		if err != nil {
			_ = file.Close()
			return err
		}
		if !opened.Mode().IsRegular() || !os.SameFile(info, opened) || opened.Size() != info.Size() {
			_ = file.Close()
			return fmt.Errorf("o arquivo mudou durante a leitura: %s", rel)
		}
		header, err := tar.FileInfoHeader(opened, "")
		if err == nil {
			header.Name = filepath.ToSlash(rel)
			included[header.Name] = true
			header.Uid, header.Gid = 0, 0
			header.Uname, header.Gname = "", ""
			err = tw.WriteHeader(header)
		}
		if err == nil {
			_, err = io.CopyN(tw, file, opened.Size())
		}
		_ = file.Close()
		return err
	})
	if err != nil {
		_ = tw.Close()
		_ = gz.Close()
		return "", err
	}
	for _, name := range requiredFiles {
		if !included[name] {
			_ = tw.Close()
			_ = gz.Close()
			return "", fmt.Errorf("%s ficou fora do contexto de compilação; confira a pasta selecionada e as exclusões do .dockerignore", name)
		}
	}
	if dockerfile != "" {
		data := []byte(dockerfile)
		if err = tw.WriteHeader(&tar.Header{Name: ".pulso.Dockerfile", Mode: 0o644, Size: int64(len(data))}); err == nil {
			_, err = tw.Write(data)
		}
		if err != nil {
			return "", err
		}
	}
	if err = tw.Close(); err != nil {
		return "", err
	}
	if err = gz.Close(); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buffer.Bytes()), nil
}

func projectDockerfile(root, kind, version string, customCommand bool) (string, []string, error) {
	switch kind {
	case "node":
		if version == "" {
			version = "24"
		}
		if version != "22" && version != "24" && version != "26" {
			return "", nil, fmt.Errorf("selecione Node.js 22, 24 ou 26")
		}
		data, err := readManifest(filepath.Join(root, "package.json"))
		if err != nil {
			return "", nil, fmt.Errorf("package.json: %w", err)
		}
		var manifest struct {
			Scripts        map[string]string `json:"scripts"`
			Main           string            `json:"main"`
			PackageManager string            `json:"packageManager"`
		}
		if err = json.Unmarshal(data, &manifest); err != nil {
			return "", nil, fmt.Errorf("package.json inválido: %w", err)
		}
		manager := "npm"
		setup := ""
		for _, candidate := range []string{"pnpm", "yarn", "bun"} {
			if strings.HasPrefix(manifest.PackageManager, candidate+"@") {
				if candidate == "bun" {
					return "", nil, fmt.Errorf("o projeto usa Bun; selecione um Dockerfile com a imagem de Bun")
				}
				manager = candidate
				packageVersion := strings.SplitN(strings.TrimPrefix(manifest.PackageManager, candidate+"@"), "+", 2)[0]
				if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.-]+)?$`).MatchString(packageVersion) {
					return "", nil, fmt.Errorf("versão de gerenciador inválida em packageManager")
				}
				if manager == "pnpm" {
					command, _ := json.Marshal([]string{"npm", "install", "-g", manager + "@" + packageVersion})
					setup = "RUN " + string(command) + "\n"
				} else {
					command, _ := json.Marshal([]string{"corepack", "prepare", "yarn@" + packageVersion, "--activate"})
					setup = "RUN npm install -g corepack && corepack enable\nRUN " + string(command) + "\n"
				}
				break
			}
		}
		command := []string{"node", manifest.Main}
		if manifest.Main == "" {
			command = []string{"node", "index.js"}
		}
		if manifest.Scripts["start"] != "" {
			command = []string{manager, "run", "start"}
		} else if manifest.Scripts["serve"] != "" {
			command = []string{manager, "run", "serve"}
		}
		cmd, _ := json.Marshal(command)
		build := ""
		if manifest.Scripts["build"] != "" {
			build = "RUN " + manager + " run build\n"
		}
		install := "RUN if [ -f package-lock.json ]; then npm ci; else npm install; fi\n"
		if manager != "npm" {
			install = "RUN " + manager + " install\n"
		}
		return "FROM node:" + version + "-bookworm-slim\nWORKDIR /app\n" + setup + "RUN mkdir -p /data && chown node:node /app /data\nUSER node\nCOPY --chown=node:node . .\n" + install + build + "ENV NODE_ENV=production\nCMD " + string(cmd) + "\n", []string{"package.json"}, nil
	case "python":
		if version == "" {
			version = "3.11"
		}
		if version != "3.11" && version != "3.12" && version != "3.13" && version != "3.14" {
			return "", nil, fmt.Errorf("selecione Python 3.11, 3.12, 3.13 ou 3.14")
		}
		entry := ""
		for _, candidate := range []string{"app.py", "main.py", "server.py"} {
			if info, err := os.Lstat(filepath.Join(root, candidate)); err == nil && info.Mode().IsRegular() {
				entry = candidate
				break
			}
		}
		if entry == "" && !customCommand {
			return "", nil, fmt.Errorf("não foi encontrado app.py, main.py ou server.py; informe o comando de início personalizado no formulário")
		}
		command := []string{"python"}
		if entry != "" {
			command = append(command, entry)
		}
		cmd, _ := json.Marshal(command)
		dependencies, err := pythonDependencyPlan(root)
		if err != nil {
			return "", nil, err
		}
		return "FROM python:" + version + "-slim-bookworm\nWORKDIR /app\nENV PYTHONUNBUFFERED=1 PYTHONDONTWRITEBYTECODE=1\n" + dependencies.setup + "RUN useradd --uid 10001 --create-home appuser && mkdir -p /data && chown appuser:appuser /app /data\nCOPY --chown=appuser:appuser . .\n" + dependencies.install + "USER appuser\nCMD " + string(cmd) + "\n", dependencies.files, nil
	default:
		return "", nil, fmt.Errorf("tipo de projeto inválido")
	}
}
