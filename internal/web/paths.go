package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"pulso.local/pulso/internal/httputil"
	"pulso.local/pulso/internal/runtime"
	"pulso.local/pulso/pkg/types"
)

type pathMapping struct {
	Host      string `json:"host"`
	Container string `json:"container"`
	Detected  bool   `json:"detected,omitempty"`
	ReadOnly  bool   `json:"read_only,omitempty"`
	Label     string `json:"label,omitempty"`
}

type pathResolver struct {
	hostRoot    string
	container   bool
	mappings    []pathMapping
	configError error
}

func newPathResolver(container bool, hostRoot, extra string) pathResolver {
	p := pathResolver{container: container}
	if container {
		p.mappings = []pathMapping{{Container: "/apps"}}
	}
	// Relative Compose paths cannot be inferred from inside the container.
	if filepath.IsAbs(hostRoot) {
		p.mappings = []pathMapping{{Host: filepath.Clean(hostRoot), Container: "/apps"}}
	}
	if strings.TrimSpace(extra) != "" {
		var more []pathMapping
		if err := json.Unmarshal([]byte(extra), &more); err != nil || len(more) > 32 {
			p.configError = errors.New("PULSO_PATH_MAPPINGS inválido: use uma lista JSON com host e container")
			return p
		}
		p.mappings = append(p.mappings, more...)
	}
	targets := make(map[string]string)
	for i, m := range p.mappings {
		if !filepath.IsAbs(m.Container) || (m.Host != "" && !filepath.IsAbs(m.Host)) || strings.ContainsRune(m.Host+m.Container, 0) {
			p.configError = errors.New("os mapeamentos de pastas precisam usar caminhos absolutos")
			return p
		}
		m.Host, m.Container = filepath.Clean(m.Host), filepath.Clean(m.Container)
		if m.Host == "." {
			m.Host = ""
		}
		if m.Host == string(filepath.Separator) || m.Container == string(filepath.Separator) {
			p.configError = errors.New("para acessar a raiz / do servidor, use o modo host com compose.host.yaml. Nesse modo, a raiz fica disponível em /host")
			return p
		}
		if previous, ok := targets[m.Host]; m.Host != "" && ok && previous != m.Container {
			p.configError = errors.New("a mesma pasta do host tem mais de um destino configurado")
			return p
		}
		targets[m.Host] = m.Container
		p.mappings[i] = m
	}
	sort.SliceStable(p.mappings, func(i, j int) bool { return len(p.mappings[i].Host) > len(p.mappings[j].Host) })
	return p
}

func pathResolverFromEnvironment() pathResolver {
	_, docker := os.Stat("/.dockerenv")
	_, podman := os.Stat("/run/.containerenv")
	container := os.Getenv("PULSO_CONTAINER") == "1" || docker == nil || podman == nil
	return newPathResolver(container, os.Getenv("PULSO_HOST_APPS_DIR"), os.Getenv("PULSO_PATH_MAPPINGS")).withHostRoot(os.Getenv("PULSO_HOST_ROOT")).withDetectedMounts()
}

func pathWithin(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func (p pathResolver) translate(path string) (string, error) {
	if p.configError != nil {
		return "", p.configError
	}
	path = strings.TrimSpace(path)
	forceHost := strings.HasPrefix(path, "host:")
	if strings.HasPrefix(path, "container:") {
		path = strings.TrimPrefix(path, "container:")
		if !filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
			return "", errors.New("use container:/caminho/absoluto")
		}
		return filepath.Clean(path), nil
	}
	if forceHost {
		path = strings.TrimPrefix(path, "host:")
		if !filepath.IsAbs(path) {
			return "", errors.New("use host:/caminho/absoluto")
		}
	}
	if strings.ContainsRune(path, 0) {
		return "", errors.New("caminho inválido")
	}
	if !filepath.IsAbs(path) {
		return path, nil
	}
	path = filepath.Clean(path)
	// A path already using a container destination must not be mapped twice.
	for _, m := range p.mappings {
		if !forceHost && pathWithin(path, m.Container) {
			return path, nil
		}
	}
	for _, m := range p.mappings {
		if m.Host != "" && pathWithin(path, m.Host) {
			rel, _ := filepath.Rel(m.Host, path)
			return filepath.Join(m.Container, rel), nil
		}
	}
	return path, nil
}

func (p pathResolver) absolute(path, cwd string) (string, error) {
	path, err := p.translate(path)
	if err != nil {
		return "", err
	}
	if path == "" {
		path = cwd
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	return filepath.Abs(path)
}

func (s *Server) prepareProcess(cfg types.ProcessConfig) (types.ProcessConfig, error) {
	var err error
	cfg.Cwd, err = s.paths.absolute(cfg.Cwd, "")
	if err != nil {
		return cfg, err
	}
	// A shell command line is not a path: translating it would rewrite words
	// inside the command. Only a script file is translated.
	shellRequested := runtime.NormalizeName(cfg.Runtime) == "shell" || cfg.Script != ""
	translate := []*string{&cfg.Entrypoint, &cfg.Interpreter}
	if shellRequested && !runtime.IsShellScriptFile(cfg.Entrypoint, cfg.Cwd) {
		translate = []*string{&cfg.Interpreter}
	}
	for _, value := range translate {
		*value, err = s.paths.commandPath(*value)
		if err != nil {
			return cfg, err
		}
	}
	cfg.EnvFile, err = s.paths.translate(cfg.EnvFile)
	if err != nil {
		return cfg, err
	}
	cfg, err = prepareWebProcess(cfg)
	if err != nil {
		return cfg, err
	}
	// Validate the command before creating files or stopping an existing application.
	executable := cfg.Entrypoint
	if cfg.Interpreter != "" {
		executable = cfg.Interpreter
	}
	if cfg.UseBundle {
		executable = "bundle"
	}
	if strings.ContainsAny(executable, "/\\") && !filepath.IsAbs(executable) {
		executable = filepath.Join(cfg.Cwd, executable)
	}
	if _, err = exec.LookPath(executable); err != nil {
		return cfg, fmt.Errorf("programa ou interpretador: %w", err)
	}
	// The shell runtime is checked by the shell itself: a command line has no
	// file to stat, and a missing script is reported in the application log.
	if cfg.Launch != nil && cfg.Runtime != "" && cfg.Runtime != "process" && cfg.Runtime != "unknown" && cfg.Runtime != "shell" {
		source := cfg.Launch.Entrypoint
		if !filepath.IsAbs(source) {
			source = filepath.Join(cfg.Cwd, source)
		}
		if _, err = os.Stat(source); err != nil {
			return cfg, fmt.Errorf("arquivo da aplicação: %w", err)
		}
	}
	return cfg, nil
}

type pathIssue struct {
	Error      string        `json:"error"`
	Code       string        `json:"code"`
	Path       string        `json:"path,omitempty"`
	Container  bool          `json:"container"`
	UID        int           `json:"uid"`
	Roots      []pathMapping `json:"roots,omitempty"`
	HostAccess bool          `json:"host_access,omitempty"`
}

func (p pathResolver) issue(err error) pathIssue {
	out := pathIssue{Error: err.Error(), Code: "invalid_configuration", Container: p.container, UID: os.Geteuid(), Roots: p.mappings, HostAccess: p.hostRoot != ""}
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		out.Path = pathErr.Path
	}
	var executableErr *exec.Error
	if out.Path == "" && errors.As(err, &executableErr) {
		out.Path = executableErr.Name
	}
	switch {
	case p.configError != nil:
		out.Code = "path_mapping_invalid"
		out.Error = p.configError.Error()
	case errors.Is(err, fs.ErrPermission):
		out.Code = "permission_denied"
		out.Error = fmt.Sprintf("O Pulso (UID %d) não tem permissão para acessar %s. Verifique o acesso à pasta e ao arquivo; em sistemas com SELinux, verifique também o rótulo do volume.", out.UID, out.Path)
	case errors.Is(err, syscall.EROFS):
		out.Code = "read_only"
		out.Error = "A pasta está montada somente para leitura. Para editar o .env, disponibilize uma pasta gravável ao Pulso."
	case errors.Is(err, syscall.ENOTDIR):
		out.Code = "path_not_directory"
		out.Error = "O caminho informado não é uma pasta: " + out.Path + ". Use Selecionar para escolher a pasta de trabalho ou o arquivo correto."
	case errors.Is(err, exec.ErrNotFound):
		out.Code = "command_missing"
		out.Error = "O programa ou interpretador não foi encontrado. Confira o tipo de execução e o campo Interpretador. Em Docker, essa dependência precisa estar instalada na imagem."
	case errors.Is(err, fs.ErrNotExist):
		out.Code = "path_not_found"
		out.Error = "A pasta ou arquivo não foi encontrado: " + out.Path + ". Confira o caminho ou use Selecionar para localizar o projeto."
		mapped := false
		for _, m := range p.mappings {
			if pathWithin(out.Path, m.Container) {
				mapped = true
				break
			}
		}
		if p.container && !mapped {
			out.Code = "container_path_unavailable"
			out.Error = "Este caminho não está disponível dentro do contêiner: " + out.Path + ". Se ele existe no servidor, configure a pasta raiz de projetos no volume Docker uma vez. Todos os projetos abaixo dela ficarão disponíveis."
		}
	}
	return out
}

type browserEntry struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Directory bool   `json:"directory"`
	Symlink   bool   `json:"symlink"`
}

// handleBrowsePaths returns metadata only; file contents stay in the admin-only dotenv API.
func (s *Server) handleBrowsePaths(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxWebBody)
	var req struct {
		Path   string `json:"path"`
		Cwd    string `json:"cwd"`
		Offset int    `json:"offset"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.settingsError(w, errors.New("caminho inválido"))
		return
	}
	if req.Offset < 0 {
		s.settingsError(w, errors.New("página inválida"))
		return
	}
	initial := req.Path
	if initial == "" {
		initial = req.Cwd
		if initial == "" && s.paths.hostRoot != "" {
			initial = s.paths.hostRoot
		}
		if initial == "" && len(s.paths.mappings) > 0 {
			initial = s.paths.mappings[0].Container
		}
	}
	cwd, err := s.paths.absolute(req.Cwd, "")
	if err != nil {
		s.settingsError(w, err)
		return
	}
	path, err := s.paths.absolute(initial, cwd)
	if err != nil {
		s.settingsError(w, err)
		return
	}
	info, err := os.Stat(path)
	if err == nil && !info.IsDir() {
		path = filepath.Dir(path)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		s.settingsError(w, err)
		return
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})
	const pageSize = 200
	start := min(req.Offset, len(entries))
	end := min(start+pageSize, len(entries))
	files := make([]browserEntry, 0, end-start)
	for _, entry := range entries[start:end] {
		entryPath := filepath.Join(path, entry.Name())
		directory, symlink := entry.IsDir(), entry.Type()&os.ModeSymlink != 0
		if symlink {
			if target, statErr := os.Stat(entryPath); statErr == nil {
				directory = target.IsDir()
			}
		}
		files = append(files, browserEntry{Name: entry.Name(), Path: s.paths.userPath(entryPath), Directory: directory, Symlink: symlink})
	}
	roots := s.paths.browserRoots(cwd)
	parent := filepath.Dir(path)
	if s.paths.hostRoot != "" && path == s.paths.hostRoot {
		parent = path
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"path": s.paths.userPath(path), "parent": s.paths.userPath(parent), "entries": files, "roots": roots, "container": s.paths.container, "host_access": s.paths.hostRoot != "", "uid": os.Geteuid(), "offset": start, "next_offset": end, "has_more": end < len(entries)})
}
