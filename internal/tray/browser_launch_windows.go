//go:build windows

package tray

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func openWindowsLogin(ctx context.Context, value string) error {
	return openWindowsLoginGuarded(ctx, value, nil)
}

func openWindowsLoginGuarded(ctx context.Context, value string, guard BrowserStartGuard) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validLoginURL(value) {
		return errors.New("no valid Tailscale device login link is available")
	}
	systemDirectory, err := windows.GetSystemDirectory()
	if err != nil {
		return errors.New("could not locate the Windows browser launcher")
	}
	// Commit dispatch by starting the system URL launcher under the guard. Shell
	// handlers may then wait for DDE/browser completion without holding ownership.
	command := exec.CommandContext(ctx, filepath.Join(systemDirectory, "rundll32.exe"), "url.dll,FileProtocolHandler", value)
	if err := runBrowserCommand(ctx, command, guard); err != nil {
		if errors.Is(err, ErrOperationCanceled) {
			return err
		}
		return errors.New("could not open the default browser for device login")
	}
	return nil
}
