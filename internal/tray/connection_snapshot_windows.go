//go:build windows

package tray

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func snapshotProcessExited(pid int) bool {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return true
	}
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	state, err := windows.WaitForSingleObject(handle, 0)
	return err == nil && state == windows.WAIT_OBJECT_0
}

// privatefs.CheckFile performs the owner-SID and ACL checks on Windows.
func snapshotOwnedByUser(os.FileInfo) bool { return true }
