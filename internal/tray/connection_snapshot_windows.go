//go:build windows

package tray

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// Lock beyond any possible configuration read. Windows byte-range locks are
// mandatory: locking byte zero would prevent the helper from reading its YAML.
func snapshotLockOffset() windows.Overlapped {
	return windows.Overlapped{Offset: 0xfffffffe, OffsetHigh: 0x7fffffff}
}

func lockConnectionSnapshot(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		_ = windows.CloseHandle(handle)
		return nil, err
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		_ = windows.CloseHandle(handle)
		return nil, errors.New("snapshot must be a regular file, not a reparse point")
	}
	offset := snapshotLockOffset()
	if err := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &offset); err != nil {
		_ = windows.CloseHandle(handle)
		return nil, err
	}
	return os.NewFile(uintptr(handle), path), nil
}

func unlockConnectionSnapshot(file *os.File) error {
	offset := snapshotLockOffset()
	return errors.Join(windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &offset), file.Close())
}

// privatefs.CheckFile performs the owner-SID and ACL checks on Windows.
func snapshotOwnedByUser(os.FileInfo) bool { return true }
