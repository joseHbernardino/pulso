package supervisor

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

func boolPointer(v bool) *bool { return &v }
func (s *Supervisor) persistChange() error {
	s.mu.RLock()
	enabled := s.autoSave && !s.shuttingDown
	s.mu.RUnlock()
	if !enabled {
		return nil
	}
	if err := s.Save(); err != nil {
		return fmt.Errorf("process changed but recovery state could not be saved: %w", err)
	}
	return nil
}

// writeStateAtomic commits a private snapshot and syncs it before acknowledging changes.
func writeStateAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".pulso-state-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		dir, err := os.Open(filepath.Dir(path))
		if err != nil {
			return err
		}
		err = dir.Sync()
		_ = dir.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
