package supervisor

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"pulso.local/pulso/internal/envfile"
	"pulso.local/pulso/pkg/types"
)

var ErrConfigConflict = errors.New("a configuração mudou; feche e abra a edição novamente")

// lockProcess serializes user operations across replacements of a managed process.
func (s *Supervisor) lockProcess(id string) (*ManagedProcess, func(), error) {
	proc, err := s.Get(id)
	if err != nil {
		return nil, nil, err
	}
	proc.operationMu.Lock()
	current, err := s.Get(proc.ID)
	if err != nil {
		proc.operationMu.Unlock()
		return nil, nil, err
	}
	return current, proc.operationMu.Unlock, nil
}

func (p *ManagedProcess) desiredConfig() types.ProcessConfig {
	p.mu.RLock()
	defer p.mu.RUnlock()
	cfg := p.Config
	if p.pending != nil {
		cfg = *p.pending
	}
	// Return an independent snapshot, including maps and nested options.
	return cloneConfig(cfg)
}

func cloneConfig(cfg types.ProcessConfig) types.ProcessConfig {
	data, _ := json.Marshal(cfg)
	var snapshot types.ProcessConfig
	_ = json.Unmarshal(data, &snapshot)
	return snapshot
}

func configRevision(cfg types.ProcessConfig) string {
	data, _ := json.Marshal(cfg)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func (s *Supervisor) ProcessSettings(id string) (types.ProcessConfig, string, error) {
	proc, err := s.Get(id)
	if err != nil {
		return types.ProcessConfig{}, "", err
	}
	cfg := proc.desiredConfig()
	return cfg, configRevision(cfg), nil
}

// UpdateSettings saves desired configuration and optionally the dotenv file.
// The running process keeps its immutable configuration until an explicit restart.
func (s *Supervisor) UpdateSettings(id string, cfg types.ProcessConfig, expected string, content *string, envRevision string) error {
	proc, unlock, err := s.lockProcess(id)
	if err != nil {
		return err
	}
	defer unlock()
	old := proc.desiredConfig()
	if expected == "" || expected != configRevision(old) {
		return ErrConfigConflict
	}
	if cfg.Name != old.Name {
		return errors.New("o nome de uma aplicação existente não pode ser alterado")
	}
	if err = cfg.Validate(); err != nil {
		return err
	}
	path, err := envfile.Resolve(cfg.EnvFile, cfg.Cwd)
	if err != nil {
		return err
	}
	cfg.EnvFile = path
	if content != nil {
		if path == "" {
			return errors.New("informe o caminho do .env antes de salvar seu conteúdo")
		}
		if _, err = envfile.Parse(*content); err != nil {
			return err
		}
	} else if _, _, err = envfile.Environment(path, cfg.Cwd, cfg.Env); err != nil {
		return err
	}
	if content != nil {
		doc, writeErr := envfile.Write(path, *content, envRevision)
		if writeErr != nil {
			return writeErr
		}
		s.ObserveEnvRevision(path, doc.Revision)
	}
	// Detach maps and nested options from the caller before storing them.
	cfg = cloneConfig(cfg)
	proc.mu.Lock()
	if configRevision(cfg) == configRevision(proc.Config) {
		proc.pending = nil
	} else {
		proc.pending = &cfg
	}
	proc.mu.Unlock()
	return s.persistChange()
}

// ObserveEnvRevision updates pending indicators for every application sharing a file.
func (s *Supervisor) ObserveEnvRevision(path, revision string) {
	s.mu.RLock()
	procs := make([]*ManagedProcess, 0, len(s.procs))
	for _, proc := range s.procs {
		procs = append(procs, proc)
	}
	s.mu.RUnlock()
	for _, proc := range procs {
		proc.mu.Lock()
		if proc.Config.EnvFile == path {
			proc.envDirty = proc.envRevision != revision
		}
		proc.mu.Unlock()
	}
}

// replacement prepares a new immutable process before stopping the old one.
func (s *Supervisor) replacement(proc *ManagedProcess) (*ManagedProcess, error) {
	cfg := proc.desiredConfig()
	if _, _, err := envfile.Environment(cfg.EnvFile, cfg.Cwd, cfg.Env); err != nil {
		return nil, fmt.Errorf("validar ambiente antes de reiniciar: %w", err)
	}
	proc.mu.RLock()
	hasPending := proc.pending != nil
	proc.mu.RUnlock()
	if !hasPending {
		return proc, nil
	}
	next := NewManagedProcess(cfg)
	next.ID, next.NumericID, next.CreatedAt = proc.ID, proc.NumericID, proc.CreatedAt
	next.Restarts = proc.Info().Restarts
	next.operationMu = proc.operationMu
	next.ApplyDefaults(s.defaults)
	next.metricsCol = s.metrics
	if s.logDir != "" {
		if err := s.setupLogWriters(next); err != nil {
			return nil, err
		}
	} else {
		proc.mu.RLock()
		next.stdoutWriter, next.stderrWriter = proc.stdoutWriter, proc.stderrWriter
		proc.mu.RUnlock()
	}
	return next, nil
}

func (s *Supervisor) installReplacement(old, next *ManagedProcess) {
	if old == next {
		return
	}
	s.mu.Lock()
	s.procs[old.ID] = next
	s.mu.Unlock()
	old.CloseLogFile()
}
