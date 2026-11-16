//go:build !linux && !darwin

package daemon

import "os"

func lockDaemon(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
}
