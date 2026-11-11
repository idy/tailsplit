//go:build !windows

package tray

import (
	"context"
	"errors"
)

func openWindowsLogin(ctx context.Context, value string) error {
	return openWindowsLoginGuarded(ctx, value, nil)
}

func openWindowsLoginGuarded(context.Context, string, BrowserStartGuard) error {
	return errors.New("Windows browser launch requires Windows")
}
