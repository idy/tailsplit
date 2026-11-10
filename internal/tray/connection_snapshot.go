package tray

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/idy/tailsplit/internal/privatefs"
)

const connectionSnapshotPrefix = ".tailsplit-connect-"

func connectionSnapshotOwner(name string) (int, bool) {
	owner, nonce, ok := strings.Cut(strings.TrimPrefix(name, connectionSnapshotPrefix), "-")
	if !strings.HasPrefix(name, connectionSnapshotPrefix) || !ok || len(nonce) < 16 || len(nonce) > 64 {
		return 0, false
	}
	for _, ch := range nonce {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9') {
			return 0, false
		}
	}
	pid, err := strconv.Atoi(owner)
	return pid, err == nil && pid > 0 && pid <= 2147483647 && strconv.Itoa(pid) == owner
}

// Only this reserved namespace is swept. Validation/Add-exit candidates, live
// owners, uncertain process states, foreign files and links are never adopted.
// A helper whose owner exited must stop; a new app never relies on its snapshot.
func cleanupConnectionSnapshots(configPath string) error {
	directory := filepath.Dir(configPath)
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	saved, _ := os.Stat(configPath)
	var failures []error
	for _, entry := range entries {
		pid, ok := connectionSnapshotOwner(entry.Name())
		if !ok || !snapshotProcessExited(pid) {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || !snapshotOwnedByUser(info) || privatefs.CheckFile(path) != nil {
			continue
		}
		if saved != nil && os.SameFile(saved, info) {
			continue
		}
		current, err := os.Lstat(path)
		if err != nil || !os.SameFile(info, current) {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
