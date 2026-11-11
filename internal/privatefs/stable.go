package privatefs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// StableDir checks every directory from the volume root before descending or
// creating a child. Thus another user cannot replace a checked private directory
// through a writable parent between publication and a helper reopening its path.
// Existing ancestors are never chmodded or adopted as private storage. The final
// directory must additionally satisfy the ordinary private-storage policy.
// With create=false, a missing directory is reported without creating anything.
func StableDir(path string, create bool) error {
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if err := stableDirectoryTree(path, create); err != nil {
		return err
	}
	if err := EnsureDir(path, path); err != nil {
		return err
	}
	return checkDirectoryACL(path, true)
}

func stableDirectoryTree(path string, create bool) error {
	parent := filepath.Dir(path)
	if parent != path {
		if err := stableDirectoryTree(parent, create); err != nil {
			return err
		}
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) && create {
		// The parent has already been checked. Exclusive private creation cannot
		// adopt a foreign entry that wins this race; check it below if it exists.
		if err := CreateDir(path); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return err
	}
	if err := checkStableDirectory(path, info); err != nil {
		return fmt.Errorf("unsafe storage ancestor %q: %w", path, err)
	}
	return nil
}
