//go:build !darwin && !linux

package tailsplit

import "errors"

func lockState(string) (func() error, error) {
	return nil, errors.New("running tailsplit currently supports macOS and Linux")
}
