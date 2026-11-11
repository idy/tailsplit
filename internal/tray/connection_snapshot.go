package tray

import (
	"crypto/rand"
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

func createConnectionSnapshot(configPath string, data []byte) (path, base string, cleanup func(), err error) {
	// Validate captured bytes against the original argument directory without
	// re-encoding them: compact YAML and aliases must keep the same byte budget.
	if _, err := startupConfig(configPath, data); err != nil {
		return "", "", nil, err
	}
	path, cleanup, err = createConnectionSnapshotBeside(configPath, data)
	if err == nil || !snapshotDirectoryUnavailable(err) {
		return path, "", cleanup, err
	}
	// This root belongs to the desktop, independently of mutable YAML state_dir.
	// Never publish an unindexed snapshot elsewhere if it is unavailable.
	fallbackPath, err := connectionSnapshotFallbackPath()
	if err != nil {
		return "", "", nil, err
	}
	base, err = filepath.Abs(filepath.Dir(configPath))
	if err != nil {
		return "", "", nil, err
	}
	directory := filepath.Dir(fallbackPath)
	if err := privatefs.StableDir(directory, true); err != nil {
		return "", "", nil, err
	}
	if err := cleanupConnectionSnapshotsAt(configPath, fallbackPath); err != nil {
		return "", "", nil, err
	}
	path, cleanup, err = createConnectionSnapshotBeside(fallbackPath, data)
	return path, base, cleanup, err
}

func createConnectionSnapshotBeside(configPath string, data []byte) (string, func(), error) {
	unlockDirectory, err := lockConnectionSnapshotDirectory(configPath)
	if err != nil {
		return "", nil, err
	}
	defer unlockDirectory()
	path := filepath.Join(filepath.Dir(configPath), fmt.Sprintf("%s%d-%s", connectionSnapshotPrefix, os.Getpid(), rand.Text()))
	file, err := privatefs.CreateFile(path)
	if err != nil {
		return "", nil, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = file.Close()
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(data); err != nil {
		return "", nil, err
	}
	if err := errors.Join(file.Sync(), file.Close()); err != nil {
		return "", nil, err
	}
	if err := privatefs.CheckFile(path); err != nil {
		return "", nil, err
	}
	lease, err := lockConnectionSnapshot(path)
	if err != nil {
		return "", nil, err
	}
	keep = true
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
	return cleanupConnectionSnapshotsAt(configPath, configPath)
}

func cleanupConnectionSnapshotsAt(configPath, snapshotPath string) error {
	directory := filepath.Dir(snapshotPath)
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
	unlockDirectory, err := lockConnectionSnapshotDirectory(snapshotPath)
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
