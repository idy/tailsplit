//go:build !darwin && !windows

package privatefs

import "os"

// POSIX ACL effective access is masked by the mode requested at creation.
func createPrivateFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
}

func checkPrivateFileACL(_ string) error { return nil }
