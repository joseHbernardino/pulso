//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd

package envfile

import "os"

func safeReadFlags() int                            { return 0 }
func preserveOwner(_ *os.File, _ os.FileInfo) error { return nil }
