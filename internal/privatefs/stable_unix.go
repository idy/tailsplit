//go:build darwin || linux

package privatefs

import (
	"errors"
	"os"
	"syscall"
)

func checkStableDirectory(path string, info os.FileInfo) error {
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("must be a directory, not a symlink")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 && stat.Uid != uint32(os.Geteuid()) {
		return errors.New("must be owned by the current user or root")
	}
	// In a trusted sticky directory (such as /tmp), only the trusted parent or
	// child owner can rename the child. Every child is checked for ownership.
	if info.Mode().Perm()&0o022 != 0 && info.Mode()&os.ModeSticky == 0 {
		return errors.New("must not be writable by other users without the sticky bit")
	}
	return checkDirectoryACL(path, false)
}
