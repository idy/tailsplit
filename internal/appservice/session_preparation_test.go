package appservice

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/idy/tailsplit"
)

func TestServiceInstallPackagePreservesFallbackOnlyWithoutServiceOrInstaller(t *testing.T) {
	for _, test := range []struct {
		name                 string
		installer, installed bool
		probeErr             error
		fallback             bool
	}{
		{"unbundled source build", false, false, ErrNotInstalled, true},
		{"wrapped absent service", false, false, fmt.Errorf("probe: %w", ErrNotInstalled), true},
		{"installed but unresponsive", false, true, ErrNotInstalled, false},
		{"protocol mismatch", false, false, errors.New("protocol mismatch"), false},
		{"build mismatch", false, false, nil, false},
		{"bundled first installation", true, false, ErrNotInstalled, false},
		{"bundled repair", true, true, ErrNotInstalled, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			executable, installed := filepath.Join(dir, "MacOS", "tailsplit"), filepath.Join(dir, "installed-service")
			installer := packagePath(executable, InstallPackage)
			for path, exists := range map[string]bool{installer: test.installer, installed: test.installed} {
				if exists {
					if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, nil, 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			for _, operation := range []string{"login", "login-new"} {
				_, err := resolveSessionService(t.Context(), operation, func(context.Context) (Info, error) {
					t.Fatal("explicit login skipped service preparation")
					return Info{}, nil
				}, func(context.Context) (Info, error) {
					path, err := serviceInstallPackage(executable, installed, test.probeErr)
					if test.installer && (err != nil || path != installer) {
						t.Fatalf("bundled installer was bypassed: %q %v", path, err)
					}
					return Info{}, err
				})
				err = installationFailure(operation, err)
				if errors.Is(err, ErrNotInstalled) != test.fallback {
					t.Fatalf("%s local-auth fallback=%t, want %t: %v", operation, errors.Is(err, ErrNotInstalled), test.fallback, err)
				}
				if !test.installer && !test.fallback && err == nil {
					t.Fatal("service mismatch or installed service was ignored")
				}
			}
		})
	}
}

func TestExplicitDeviceLoginPreparesItsServiceBeforeOpeningASession(t *testing.T) {
	for _, operation := range []string{"start", "login", "login-new"} {
		t.Run(operation, func(t *testing.T) {
			probed, prepared := 0, 0
			info, err := resolveSessionService(t.Context(), operation, func(context.Context) (Info, error) {
				probed++
				return Info{BuildID: "old-service"}, nil
			}, func(context.Context) (Info, error) {
				prepared++ // EnsureInstalled checks the build and obtains approval if needed.
				return Info{BuildID: "this-app"}, nil
			})
			if err != nil || info.BuildID != "this-app" || prepared != 1 || probed != 0 {
				t.Fatalf("explicit action did not use prepared service: %+v %v probes=%d preparations=%d", info, err, probed, prepared)
			}
		})
	}
}

func TestBackgroundAndMaintenanceOperationsNeverOpenInstaller(t *testing.T) {
	for _, operation := range []string{"discover", "ping", "logout", "status", "unknown"} {
		for _, failure := range []error{nil, ErrNotInstalled, errors.New("old protocol")} {
			prepared := false
			info, err := resolveSessionService(t.Context(), operation, func(context.Context) (Info, error) {
				return Info{BuildID: "existing"}, failure
			}, func(context.Context) (Info, error) { prepared = true; return Info{}, nil })
			if prepared || !errors.Is(err, failure) || info.BuildID != "existing" {
				t.Fatalf("%s triggered installation or hid probe error: %+v %v", operation, info, err)
			}
		}
	}
}

func TestCanceledOpenSessionCannotFallBackOrRequestSetup(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, operation := range []string{"start", "login", "login-new", "discover", "ping", "logout"} {
		connection, err := OpenSession(ctx, "unused", operation, tailsplit.Config{}, io.Discard)
		if connection != nil || err != context.Canceled {
			t.Fatalf("canceled %s contacted the service: %v", operation, err)
		}
		if event := ErrorEvent(err); event["code"] != "" {
			t.Fatalf("canceled %s offered recovery or fallback: %v", operation, event)
		}
	}
}

func TestCanceledOrFailedInstallationCannotContinueLogin(t *testing.T) {
	failure := errors.New("installer failed")
	for _, operation := range []string{"start", "login", "login-new"} {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		called := false
		resolve := func(context.Context) (Info, error) { called = true; return Info{}, nil }
		if _, err := resolveSessionService(ctx, operation, resolve, resolve); !errors.Is(err, context.Canceled) || called {
			t.Fatalf("canceled %s opened service: %v", operation, err)
		}
		ctx, cancel = context.WithCancel(t.Context())
		_, err := resolveSessionService(ctx, operation, resolve, func(context.Context) (Info, error) { cancel(); return Info{BuildID: "matching"}, nil })
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("late installer success continued %s: %v", operation, err)
		}
		_, err = resolveSessionService(t.Context(), operation, resolve, func(context.Context) (Info, error) { return Info{}, failure })
		if !errors.Is(err, failure) {
			t.Fatalf("installer error hidden for %s: %v", operation, err)
		}
	}
}
