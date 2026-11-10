package tray

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/privatefs"
)

const connectionSnapshotPrefix = ".tailsplit-connect-"

// The PID is a diagnostic label only. A held OS file lock, never PID existence,
// identifies a live snapshot across PID reuse, process crashes and reboots.
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
	return pid, err == nil && pid > 0 && uint64(pid) <= 4294967295 && strconv.Itoa(pid) == owner
}

// Creation and sweeping share this short-lived gate: a new snapshot cannot be
// observed between writing its bytes and acquiring its lifetime lock.
func lockConnectionSnapshotDirectory(configPath string) (func() error, error) {
	return tailsplit.LockSavedState(filepath.Join(filepath.Dir(configPath), ".tailsplit-connection-lock"))
}

func createConnectionSnapshot(configPath string, data []byte) (string, func(), error) {
	unlockDirectory, err := lockConnectionSnapshotDirectory(configPath)
	if err != nil {
		return "", nil, err
	}
	defer unlockDirectory()
	path, _, err := configCandidatePrefix(configPath, data, fmt.Sprintf("%s%d-", connectionSnapshotPrefix, os.Getpid()))
	if err != nil {
		return "", nil, err
	}
	lease, err := lockConnectionSnapshot(path)
	if err != nil {
		_ = os.Remove(path)
		return "", nil, err
	}
	cleanup := sync.OnceFunc(func() {
		_ = unlockConnectionSnapshot(lease)
		_ = os.Remove(path)
	})
	return path, cleanup, nil
}

// Only this reserved namespace is swept. Validation/Add-exit candidates, locked
// snapshots, foreign files and links are never adopted. The OS releases a dead
// creator's lock, even when an unrelated live process has since reused its PID.
func cleanupConnectionSnapshots(configPath string) error {
	directory := filepath.Dir(configPath)
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	found := false
	for _, entry := range entries {
		if _, ok := connectionSnapshotOwner(entry.Name()); ok {
			found = true
			break
		}
	}
	if !found {
		return nil // Do not create snapshot metadata for unrelated desktop operations.
	}
	unlockDirectory, err := lockConnectionSnapshotDirectory(configPath)
	if err != nil {
		return err
	}
	defer unlockDirectory()
	// Rescan under the gate; the preliminary scan never authorizes a deletion.
	entries, err = os.ReadDir(directory)
	if err != nil {
		return err
	}
	saved, _ := os.Stat(configPath)
	var failures []error
	for _, entry := range entries {
		if _, ok := connectionSnapshotOwner(entry.Name()); !ok {
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
		lease, err := lockConnectionSnapshot(path)
		if err != nil {
			continue
		} // A live creator or an uncertain access result.
		locked, lockErr := lease.Stat()
		current, statErr := os.Lstat(path)
		matches := lockErr == nil && statErr == nil && os.SameFile(info, locked) && os.SameFile(locked, current)
		_ = unlockConnectionSnapshot(lease)
		// The directory gate prevents publication/reuse during this final unlink.
		if matches {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				failures = append(failures, err)
			}
		}
	}
	return errors.Join(failures...)
}
