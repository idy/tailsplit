//go:build !darwin && !linux && !windows

package tray

import "os"

// Unknown platforms cannot establish that an owner has exited.
func snapshotProcessExited(int) bool       { return false }
func snapshotOwnedByUser(os.FileInfo) bool { return false }
