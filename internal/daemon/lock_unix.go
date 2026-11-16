//go:build linux || darwin

package daemon

import (
	"fmt"
	"os"
	"syscall"
)

func lockDaemon(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("another daemon owns this data directory: %w", err)
	}
	return f, nil
}
