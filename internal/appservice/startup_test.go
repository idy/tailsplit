package appservice

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStartupUpgradeOnlyTouchesPreviouslyInstalledPackagedService(t *testing.T) {
	for _, test := range []struct {
		name                 string
		installer, installed bool
		calls                int
	}{
		{"development build", false, true, 0},
		{"first installation", true, false, 0},
		{"no installed app service", false, false, 0},
		{"existing packaged service", true, true, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			installer, installed := filepath.Join(dir, "service.pkg"), filepath.Join(dir, "service")
			if test.installer {
				if err := os.WriteFile(installer, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if test.installed {
				if err := os.WriteFile(installed, nil, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			if err := upgradeExisting(context.Background(), installer, installed, func(context.Context) error { calls++; return nil }); err != nil {
				t.Fatal(err)
			}
			if calls != test.calls {
				t.Fatalf("ensure calls=%d, want %d", calls, test.calls)
			}
		})
	}
}

func TestStartupUpgradePreservesCancellationAndInstallerFailure(t *testing.T) {
	dir := t.TempDir()
	installer, installed := filepath.Join(dir, "service.pkg"), filepath.Join(dir, "service")
	for _, path := range []string{installer, installed} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	if err := upgradeExisting(ctx, installer, installed, func(context.Context) error { called = true; return nil }); !errors.Is(err, context.Canceled) || called {
		t.Fatal("canceled startup reached Installer", err)
	}
	failed := errors.New("installer was canceled")
	if err := upgradeExisting(context.Background(), installer, installed, func(context.Context) error { return failed }); !errors.Is(err, failed) {
		t.Fatal("installer error was hidden", err)
	}
}
