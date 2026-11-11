//go:build windows

package tray

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/windows"
)

func snapshotDirectoryUnavailable(err error) bool {
	return errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.EROFS) || errors.Is(err, windows.ERROR_WRITE_PROTECT)
}
