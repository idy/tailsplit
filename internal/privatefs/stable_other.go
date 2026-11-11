//go:build !darwin && !linux && !windows

package privatefs

import (
	"errors"
	"os"
)

func checkStableDirectory(_ string, _ os.FileInfo) error {
	return errors.New("stable private storage is unsupported on this platform")
}
