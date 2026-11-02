//go:build !darwin || !cgo

package tailsplit

import (
	"errors"
	"runtime"
)

func installSystemDNS(string) (func() error, error) {
	if runtime.GOOS == "darwin" {
		return nil, errors.New("macOS TUN system DNS support requires a CGO-enabled build")
	}
	// Other platforms use sing-tun's native DNS integration.
	return func() error { return nil }, nil
}
