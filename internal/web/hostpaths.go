package web

import (
	"errors"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"pulso.local/pulso/pkg/types"
)

// Full-host access is explicitly enabled by the host deployment override.
// The host root is mounted beneath a separate prefix so image runtimes remain usable.
func (p pathResolver) withHostRoot(root string) pathResolver {
	if strings.TrimSpace(root) == "" {
		return p
	}
	if p.configError != nil {
		return p
	}
	if !p.container || !filepath.IsAbs(root) || filepath.Clean(root) == string(filepath.Separator) || strings.ContainsRune(root, 0) {
		p.configError = errors.New("PULSO_HOST_ROOT deve apontar para o destino absoluto do volume do host, por exemplo /host")
		return p
	}
	p.hostRoot = filepath.Clean(root)
	p.mappings = append(p.mappings, pathMapping{Host: string(filepath.Separator), Container: p.hostRoot, Label: "Servidor"})
	sort.SliceStable(p.mappings, func(i, j int) bool { return len(p.mappings[i].Host) > len(p.mappings[j].Host) })
	return p
}

// userPath keeps paths in the image unambiguous when absolute input defaults to the host.
func (p pathResolver) userPath(path string) string {
	if p.hostRoot == "" || path == "" || !filepath.IsAbs(path) {
		return path
	}
	for _, m := range p.mappings {
		if pathWithin(path, m.Container) {
			return path
		}
	}
	return "container:" + path
}

func (p pathResolver) clientConfig(cfg types.ProcessConfig) types.ProcessConfig {
	cfg.Cwd = p.userPath(cfg.Cwd)
	cfg.EnvFile = p.userPath(cfg.EnvFile)
	cfg.Entrypoint = p.userPath(cfg.Entrypoint)
	cfg.Interpreter = p.userPath(cfg.Interpreter)
	if cfg.Launch != nil {
		launch := *cfg.Launch
		launch.Entrypoint = p.userPath(launch.Entrypoint)
		launch.Interpreter = p.userPath(launch.Interpreter)
		cfg.Launch = &launch
	}
	return cfg
}

func (p pathResolver) commandPath(path string) (string, error) {
	if p.hostRoot != "" && !strings.HasPrefix(path, "host:") && !strings.HasPrefix(path, "container:") {
		// Explicit image interpreters and commands must not be replaced by host binaries.
		for _, dir := range []string{"/bin", "/sbin", "/usr/bin", "/usr/sbin", "/usr/local/bin"} {
			if pathWithin(path, dir) {
				if _, err := exec.LookPath(path); err == nil {
					return path, nil
				}
			}
		}
	}
	return p.translate(path)
}

func (p pathResolver) browserRoots(cwd string) []pathMapping {
	roots := append([]pathMapping(nil), p.mappings...)
	if p.hostRoot != "" {
		roots = append(roots, pathMapping{Container: "container:/", Label: "Ambiente do Pulso"})
	}
	if len(roots) == 0 {
		roots = []pathMapping{{Container: cwd}, {Container: string(filepath.Separator)}}
	}
	return roots
}
