//go:build !windows

package tailsplit

import (
	"os"
	"path/filepath"
)

func commitRuleCache(source, target string) (bool, error) {
	if err := os.Rename(source, target); err != nil {
		return false, err
	}
	dir, err := os.Open(filepath.Dir(target))
	if err != nil {
		return true, err
	}
	defer dir.Close()
	return true, dir.Sync()
}
