//go:build windows

package tray

import (
	"context"
	"errors"

	"golang.org/x/sys/windows"
)

func openWindowsLogin(ctx context.Context, value string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validLoginURL(value) {
		return errors.New("no valid Tailscale device login link is available")
	}
	verb, _ := windows.UTF16PtrFromString("open")
	url, err := windows.UTF16PtrFromString(value)
	if err != nil {
		return errors.New("invalid device login link")
	}
	if err := windows.ShellExecute(0, verb, url, nil, nil, windows.SW_SHOWNORMAL); err != nil {
		return errors.New("could not open the default browser for device login")
	}
	return nil
}
