//go:build !darwin && !linux && !windows

package tray

import (
	"errors"
	"os"
)

func lockConnectionSnapshot(string) (*os.File, error) {
	return nil, errors.New("snapshot locks are unsupported on this platform")
}
func unlockConnectionSnapshot(file *os.File) error { return file.Close() }
func snapshotOwnedByUser(os.FileInfo) bool         { return false }
