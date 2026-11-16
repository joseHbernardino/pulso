//go:build linux || darwin || freebsd || openbsd || netbsd

package envfile

import (
	"os"
	"syscall"
)

func safeReadFlags() int { return syscall.O_NOFOLLOW | syscall.O_NONBLOCK }
func preserveOwner(f *os.File, info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	current, err := f.Stat()
	if err != nil {
		return err
	}
	owner, ok := current.Sys().(*syscall.Stat_t)
	if ok && owner.Uid == stat.Uid && owner.Gid == stat.Gid {
		return nil
	}
	return f.Chown(int(stat.Uid), int(stat.Gid))
}
