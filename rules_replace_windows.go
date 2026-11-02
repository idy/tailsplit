//go:build windows

package tailsplit

import "golang.org/x/sys/windows"

func commitRuleCache(source, target string) (bool, error) {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return false, err
	}
	to, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return false, err
	}
	// Both paths are in state_dir. Windows cannot fsync a directory; use its
	// same-volume atomic replacement with write-through after syncing the file.
	err = windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
	return err == nil, err
}
