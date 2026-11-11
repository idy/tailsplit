//go:build !darwin

package privatefs

// Linux POSIX ACL write masks are reflected in the mode bits; Windows directory
// ACLs are checked by checkStableDirectory and EnsureDir.
func checkDirectoryACL(_ string, _ bool) error { return nil }
