//go:build darwin || linux

package tray

import (
	"errors"
	"os"
	"syscall"
)

func snapshotProcessExited(pid int) bool {
	// EPERM and every other uncertain result preserve the file.
	return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

func snapshotOwnedByUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid())
}
