//go:build windows

package tailsplit

import (
	"errors"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/windows"
)

func lockState(directory string) (func() error, error) {
	path := filepath.Join(directory, "runner.lock")
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	// Do not share deletion: replacing an open lock file would let a second
	// process lock a different file at the same path. Never follow a reparse point.
	handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_ALWAYS,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open state lock", Path: path, Err: err}
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		_ = windows.CloseHandle(handle)
		return nil, err
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		_ = windows.CloseHandle(handle)
		return nil, errors.New("state lock must be a regular file, not a reparse point")
	}
	var overlapped windows.Overlapped
	if err := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, &overlapped); err != nil {
		_ = windows.CloseHandle(handle)
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, errors.New("state_dir is already in use by another tailsplit process")
		}
		return nil, &os.PathError{Op: "lock state", Path: path, Err: err}
	}
	return sync.OnceValue(func() error {
		return errors.Join(windows.UnlockFileEx(handle, 0, 1, 0, &overlapped), windows.CloseHandle(handle))
	}), nil
}
