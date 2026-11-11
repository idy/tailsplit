//go:build darwin || linux

package privatefs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func stableTestRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestStableDirRejectsReplaceableAncestorsWithoutChangingThem(t *testing.T) {
	for _, mode := range []os.FileMode{0o777, 0o770} {
		for _, create := range []bool{false, true} {
			t.Run(mode.String()+map[bool]string{false: "-cleanup", true: "-create"}[create], func(t *testing.T) {
				parent := filepath.Join(stableTestRoot(t), "shared")
				root := filepath.Join(parent, "private")
				if err := StableDir(root, true); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(parent, mode); err != nil {
					t.Fatal(err)
				}
				if err := StableDir(root, create); err == nil {
					t.Fatal("accepted a 0700 root replaceable through its writable parent")
				}
				if info, err := os.Stat(parent); err != nil || info.Mode().Perm() != mode {
					t.Fatal("modified a shared parent instead of rejecting it", err)
				}
			})
		}
	}
}

func TestStableDirAllowsTrustedStickyParentAndPrivateChildren(t *testing.T) {
	parent := filepath.Join(stableTestRoot(t), "shared")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, os.ModeSticky|0o777); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "private", "snapshots")
	if err := StableDir(root, true); err != nil {
		t.Fatal("sticky parent prevented private storage creation", err)
	}
	if err := StableDir(root, false); err != nil {
		t.Fatal(err)
	}
}

func TestStableDirRejectsSymlinkAncestorsAndDoesNotCreateOnCleanup(t *testing.T) {
	parent := stableTestRoot(t)
	target := filepath.Join(parent, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	for _, create := range []bool{false, true} {
		if err := StableDir(filepath.Join(link, "snapshots"), create); err == nil {
			t.Fatal("followed a symlinked storage ancestor")
		}
	}
	if entries, err := os.ReadDir(target); err != nil || len(entries) != 0 {
		t.Fatal("touched a symlink target", err)
	}
	missing := filepath.Join(parent, "missing", "snapshots")
	if err := StableDir(missing, false); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing cleanup root should retain the not-exist result", err)
	}
	if _, err := os.Stat(filepath.Dir(missing)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cleanup created snapshot metadata", err)
	}
}
