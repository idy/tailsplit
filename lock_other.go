//go:build !darwin && !linux && !windows

package tailsplit

import "errors"

func lockState(string) (func() error, error) {
	return nil, errors.New("running tailsplit currently supports macOS, Linux and Windows")
}
