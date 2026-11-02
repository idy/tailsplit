//go:build !windows

package tray

import (
	"context"
	"errors"
)

func openWindowsLogin(context.Context, string) error {
	return errors.New("Windows browser launch requires Windows")
}
