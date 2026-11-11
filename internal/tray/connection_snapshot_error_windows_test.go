//go:build windows

package tray

import (
	"fmt"
	"os"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsSnapshotDirectoryErrors(t *testing.T) {
	for _, test := range []struct {
		name     string
		err      error
		fallback bool
	}{
		{"write-protected media", windows.ERROR_WRITE_PROTECT, true},
		{"access denied", windows.ERROR_ACCESS_DENIED, true},
		{"read-only filesystem", syscall.EROFS, true},
		{"permission", os.ErrPermission, true},
		{"full volume", windows.ERROR_DISK_FULL, false},
		{"sharing violation", windows.ERROR_SHARING_VIOLATION, false},
		{"invalid path", windows.ERROR_INVALID_NAME, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			wrapped := fmt.Errorf("create connection snapshot: %w", &os.PathError{Op: "open", Path: "config snapshot", Err: test.err})
			if got := snapshotDirectoryUnavailable(wrapped); got != test.fallback {
				t.Fatalf("fallback=%v, want %v for %v", got, test.fallback, wrapped)
			}
		})
	}
}
