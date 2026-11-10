package tray

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/privatefs"
)

// RebuildConfigDocument backs up the saved bytes and replaces them with the
// desktop defaults. Callers decide whether the document needs rebuilding.
// A nonempty matching revision is required. Once complete, the private backup is
// retained and its path is returned even if saving the new configuration fails.
func RebuildConfigDocument(path, revision string) (backupPath string, err error) {
	original, current, err := ReadConfigDocument(path)
	if err != nil {
		return "", err
	}
	if revision == "" || current != revision {
		return "", ErrConfigChanged
	}

	name := filepath.Base(path) + ".backup-" + time.Now().UTC().Format("20060102T150405Z") + "-" + rand.Text()
	backup, err := privatefs.CreateFile(filepath.Join(filepath.Dir(path), name))
	if err != nil {
		return "", fmt.Errorf("create configuration backup: %w", err)
	}
	complete := false
	defer func() {
		if !complete {
			_ = backup.Close()
			_ = os.Remove(backup.Name())
		}
	}()
	if _, err := backup.Write(original); err != nil {
		return "", fmt.Errorf("write configuration backup: %w", err)
	}
	if err := errors.Join(backup.Sync(), backup.Close()); err != nil {
		return "", fmt.Errorf("finish configuration backup: %w", err)
	}
	if err := privatefs.CheckFile(backup.Name()); err != nil {
		return "", fmt.Errorf("check configuration backup: %w", err)
	}
	complete = true

	return backup.Name(), SaveConfigDocument(path, revision, tailsplit.DefaultYAML)
}
