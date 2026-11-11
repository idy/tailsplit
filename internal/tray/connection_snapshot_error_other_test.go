//go:build !windows

package tray

import (
	"fmt"
	"os"
	"syscall"
	"testing"
)

func TestSnapshotDirectoryErrors(t *testing.T) {
	for _, test := range []struct {
		err      error
		fallback bool
	}{{os.ErrPermission, true}, {syscall.EACCES, true}, {syscall.EROFS, true}, {syscall.ENOSPC, false}, {syscall.EINVAL, false}} {
		wrapped := fmt.Errorf("create snapshot: %w", &os.PathError{Op: "open", Path: "config snapshot", Err: test.err})
		if got := snapshotDirectoryUnavailable(wrapped); got != test.fallback {
			t.Fatalf("fallback=%v, want %v for %v", got, test.fallback, wrapped)
		}
	}
}
