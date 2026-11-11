//go:build !windows

package tray

import (
	"errors"
	"os"
	"syscall"
)

func snapshotDirectoryUnavailable(err error) bool {
	return errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.EROFS)
}
