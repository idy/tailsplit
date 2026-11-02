//go:build !windows

package privatefs

import "os"

// Replace atomically installs a completed file on the same filesystem.
func Replace(source, target string) error { return os.Rename(source, target) }
