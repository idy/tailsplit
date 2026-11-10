package appservice

import (
	"context"
	"errors"
	"io"
	"os"
	"runtime"
)

// UpgradeExisting prepares a previously installed service at desktop startup.
// First installation remains an explicit Connect action. Development binaries
// without a bundled installer never open Installer. EnsureInstalled still checks
// the live root-owned service and preserves macOS administrator approval.
func UpgradeExisting(ctx context.Context, executable string, output io.Writer) error {
	if runtime.GOOS != "darwin" {
		return nil // Windows Setup upgrades its service before restarting the app.
	}
	return upgradeExisting(ctx, packagePath(executable, InstallPackage), ExecutablePath, func(ctx context.Context) error {
		_, err := EnsureInstalled(ctx, executable, output)
		return err
	})
}

func upgradeExisting(ctx context.Context, installer, installed string, ensure func(context.Context) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, path := range []string{installer, installed} {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("network service startup paths must be regular files")
		}
	}
	return ensure(ctx)
}
