//go:build !windows

package privatefs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateDirectoryRejectsPublicPermissionsWithoutChangingThem(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDir(path); err == nil {
		t.Fatal("accepted public state directory")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatal("changed existing directory permissions:", info, err)
	}
}

func TestPrivateDirectoryRejectsSymlink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")
	if err := os.Symlink(t.TempDir(), path); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDir(path); err == nil {
		t.Fatal("accepted symlink as state directory")
	}
}
