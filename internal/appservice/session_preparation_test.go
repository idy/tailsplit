package appservice

import (
	"context"
	"errors"
	"testing"
)

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
