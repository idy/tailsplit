//go:build !windows

package appservice

import (
	"errors"
	"os"
	"syscall"
)

// The service owns its runtime directory. Restricting a root process to a
// user-writable state directory is insufficient: that user could swap a cache
// or state file for a symlink while the root engine is opening it.
func privateDirectory(path string) error {
	if err := os.Mkdir(path, 0o700); err != nil && !os.IsExist(err) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
		int(owner.Uid) != os.Geteuid() || info.Mode().Perm()&0o077 != 0 {
		return errors.New("network service storage must be a private directory owned by the service")
	}
	return nil
}
