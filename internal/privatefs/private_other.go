//go:build !windows

// Package privatefs creates and checks private CLI configuration and state.
package privatefs

import (
	"errors"
	"os"
)

// EnsureDir checks only path itself here. Windows also walks the contents,
// except those of foreign directories that other software manages.
func EnsureDir(path string, foreign ...string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("state_dir must be a directory, not a symlink")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return errors.New("state_dir must have owner-only permissions (chmod 700)")
	}
	return nil
}

// CreateDir exclusively creates a private directory. The parent must exist.
// An existing entry is never adopted or changed, even when it is empty.
func CreateDir(path string) error {
	return os.Mkdir(path, 0o700)
}

func CreateFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
}

func CheckFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return errors.New("private file must be regular with owner-only permissions (chmod 600)")
	}
	return nil
}
