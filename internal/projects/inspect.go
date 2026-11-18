// Package projects discovers launch suggestions without executing project code.
package projects

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"pulso.local/pulso/pkg/types"
)

const maxManifest = 128 << 10

type Suggestion struct {
	Title     string              `json:"title"`
	Config    types.ProcessConfig `json:"config"`
	Available bool                `json:"available"`
	Missing   []string            `json:"missing,omitempty"`
}

type Report struct {
	Path        string       `json:"path"`
	Name        string       `json:"name"`
	Suggestions []Suggestion `json:"suggestions"`
	EnvFiles    []string     `json:"env_files"`
	Notes       []string     `json:"notes"`
}

// Inspect reads a bounded selection of regular files. It never imports code,
// evaluates package scripts, installs dependencies or reads dotenv contents.
func Inspect(directory string, lookPath func(string) (string, error)) (Report, error) {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	root, err := os.Open(directory)
	if err != nil {
		return Report{}, err
	}
	defer func() { _ = root.Close() }()
	info, err := root.Stat()
	if err != nil {
		return Report{}, err
	}
	if !info.IsDir() {
		return Report{}, fmt.Errorf("selecione a pasta do projeto")
	}
	entries, err := root.ReadDir(513)
	if err != nil && err != io.EOF {
		return Report{}, err
	}
	name := regexp.MustCompile(`[^A-Za-z0-9._-]+`).ReplaceAllString(filepath.Base(directory), "-")
	name = strings.Trim(name, ".-_")
	if name == "" {
		name = "minha-aplicacao"
	}
	if len(name) > 100 {
		name = name[:100]
	}
	report := Report{Path: directory, Name: name, Suggestions: []Suggestion{}, EnvFiles: []string{}, Notes: []string{}}
	if len(entries) > 512 {
		entries = entries[:512]
		report.Notes = append(report.Notes, "A pasta tem muitos arquivos; a busca de arquivos .env foi limitada. Você pode selecionar outro arquivo manualmente.")
	}
	for _, entry := range entries {
		n := entry.Name()
		if entry.Type().IsRegular() && (n == ".env" || strings.HasPrefix(n, ".env.") || strings.HasSuffix(n, ".env")) && !strings.HasSuffix(n, ".example") && !strings.HasSuffix(n, ".sample") && !strings.HasSuffix(n, ".template") && !strings.HasSuffix(n, ".dist") && len(report.EnvFiles) < 32 {
			report.EnvFiles = append(report.EnvFiles, filepath.Join(directory, n))
		}
	}
	sort.Strings(report.EnvFiles)
	exists := func(name string) bool {
		info, err := os.Lstat(filepath.Join(directory, name))
		return err == nil && info.Mode().IsRegular()
	}
	add := func(title, runtime, entrypoint, interpreter string, args []string, requirements ...string) {
		missing := []string{}
		for _, command := range requirements {
			if _, err := lookPath(command); err != nil {
				missing = append(missing, command)
			}
		}
		report.Suggestions = append(report.Suggestions, Suggestion{Title: title, Available: len(missing) == 0, Missing: missing,
			Config: types.ProcessConfig{Name: name, Runtime: runtime, Entrypoint: entrypoint, Interpreter: interpreter, Args: args, Cwd: directory, RestartPolicy: types.RestartOnFailure, MaxRestarts: 10}})
	}
	if exists("package.json") {
		var manifest struct {
			Scripts        map[string]string `json:"scripts"`
			PackageManager string            `json:"packageManager"`
			Engines        map[string]string `json:"engines"`
		}
		data, err := readManifest(filepath.Join(directory, "package.json"))
		if err != nil {
			report.Notes = append(report.Notes, "Não foi possível analisar package.json: "+err.Error())
		} else if err = json.Unmarshal(data, &manifest); err != nil {
			report.Notes = append(report.Notes, "O package.json contém JSON inválido. Corrija-o antes de instalar as dependências.")
		} else {
			manager := "npm"
			for _, candidate := range []string{"pnpm", "yarn", "bun"} {
				if strings.HasPrefix(manifest.PackageManager, candidate+"@") || (manifest.PackageManager == "" && (exists(candidate+".lock") || (candidate == "pnpm" && exists("pnpm-lock.yaml")) || (candidate == "bun" && exists("bun.lockb")))) {
					manager = candidate
					break
				}
			}
			for _, script := range []string{"start", "serve", "dev"} {
				if strings.TrimSpace(manifest.Scripts[script]) != "" {
					title := "Node.js · " + manager + " run " + script
					if script == "dev" {
						title += " (desenvolvimento)"
					}
					add(title, "process", manager, "", []string{"run", script}, manager)
				}
			}
			if version := manifest.Engines["node"]; version != "" {
				report.Notes = append(report.Notes, "O projeto declara Node.js "+version+". Confira a compatibilidade com a versão do ambiente; a análise não executa o projeto.")
			}
		}
		if stat, err := os.Stat(filepath.Join(directory, "node_modules")); err != nil || !stat.IsDir() {
			report.Notes = append(report.Notes, "As dependências JavaScript ainda não estão disponíveis em node_modules. Elas precisam ser instaladas antes de iniciar.")
		}
	}
	python := "python3"
	interpreter := ""
	for _, venv := range []string{".venv/bin/python", "venv/bin/python"} {
		candidate := filepath.Join(directory, venv)
		if _, err := lookPath(candidate); err == nil {
			python, interpreter = candidate, candidate
			break
		}
	}
	for _, file := range []string{"app.py", "main.py", "server.py"} {
		if exists(file) {
			add("Python · "+file, "python", filepath.Join(directory, file), interpreter, nil, python)
		}
	}
	if exists("requirements.txt") || exists("pyproject.toml") {
		report.Notes = append(report.Notes, "O projeto declara dependências Python. Use um ambiente virtual compatível com o Python do contêiner; bibliotecas do host não são automaticamente disponibilizadas.")
	}
	for _, file := range []string{"server.js", "index.js", "app.js"} {
		if exists(file) {
			add("Node.js · "+file, "node", filepath.Join(directory, file), "", nil, "node")
		}
	}
	for _, file := range []string{"scripts/app.sh", "start.sh", "app.sh"} {
		if exists(file) {
			data, err := readManifest(filepath.Join(directory, file))
			shell := "sh"
			if err == nil {
				first, _, _ := strings.Cut(string(data), "\n")
				if strings.HasPrefix(first, "#!") && (strings.HasSuffix(strings.TrimSpace(first), "/bash") || strings.Contains(first, "env bash")) {
					shell = "bash"
				}
			}
			requirements := []string{shell}
			if err == nil {
				// Inspect common interpreter invocations, without evaluating shell text.
				seen := map[string]bool{"sh": true}
				pattern := regexp.MustCompile(`(?m)^[\t ]*(?:exec[\t ]+)?(python[23]?(?:\.[0-9]+)?|node|npm|pnpm|yarn|bun|deno|ruby|php)(?:[\t ]|$)`)
				for _, match := range pattern.FindAllStringSubmatch(string(data), -1) {
					if !seen[match[1]] {
						requirements = append(requirements, match[1])
						seen[match[1]] = true
					}
				}
			}
			add("Script · "+file, "process", shell, "", []string{filepath.Join(directory, file)}, requirements...)
		}
	}
	if exists("go.mod") {
		add("Go · go run .", "process", "go", "", []string{"run", "."}, "go")
	}
	if exists("Dockerfile") || exists("compose.yaml") || exists("docker-compose.yml") || exists("compose.yml") {
		report.Notes = append(report.Notes, "Este projeto também contém configuração Docker. As sugestões abaixo executam processos no ambiente do Pulso; elas não executam o Dockerfile ou o Compose do projeto.")
	}
	if len(report.Suggestions) == 0 {
		report.Notes = append(report.Notes, "Nenhum comando de início conhecido foi encontrado. Informe o executável ou script na seção Execução.")
	}
	report.Notes = append(report.Notes, "Análise estática: verifica arquivos e comandos disponíveis, sem executar código nem validar todas as dependências.")
	return report, nil
}

func readManifest(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("use um arquivo regular")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxManifest+1))
	if len(data) > maxManifest {
		return nil, fmt.Errorf("arquivo maior que 128 KiB")
	}
	return data, err
}
