package appservice

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestServiceInstallationErrorsRetainDiagnosticsAndExposeRecovery(t *testing.T) {
	original := errors.New("update the TailSplit network service from this app before continuing")
	wrapped := fmt.Errorf("prepare login: %w", installationFailure("login", original))
	if !errors.Is(wrapped, original) || !errors.Is(wrapped, ErrNeedsInstallation) {
		t.Fatal("service error lost identity", wrapped)
	}
	event := ErrorEvent(wrapped)
	if event["code"] != "service-installation" || event["message"] != wrapped.Error() {
		t.Fatal("missing actionable error", event)
	}
}

func TestWindowsMissingServiceRequiresManualSetupOnlyForExplicitActions(t *testing.T) {
	for _, operation := range []string{"start", "login", "login-new", "discover", "ping", "logout", "status", "unknown"} {
		t.Run(operation, func(t *testing.T) {
			for _, failure := range []error{nil, ErrNotInstalled, fmt.Errorf("Windows probe: %w", ErrNotInstalled), errors.New("protocol mismatch"), context.Canceled, context.DeadlineExceeded, errors.Join(ErrNotInstalled, context.Canceled), errors.Join(ErrNotInstalled, context.DeadlineExceeded)} {
				err := windowsServiceFailure(operation, failure)
				wantSetup := (operation == "start" || operation == "login" || operation == "login-new") && errors.Is(failure, ErrNotInstalled)
				wantSetup = wantSetup && !errors.Is(failure, context.Canceled) && !errors.Is(failure, context.DeadlineExceeded)
				if !errors.Is(err, failure) || errors.Is(err, ErrNeedsInstallation) || errors.Is(err, ErrServiceSetupRequired) != wantSetup {
					t.Fatalf("incorrect Windows service failure: %v", err)
				}
				if !wantSetup && err != failure {
					t.Fatalf("ordinary Windows error was changed: %v", err)
				}
				if err == nil {
					continue
				}
				event := ErrorEvent(fmt.Errorf("%s: %w", operation, err))
				wantCode := ""
				if wantSetup {
					wantCode = "service-setup-required"
					if !strings.Contains(event["message"], "rerun TailSplit Setup") {
						t.Fatalf("manual setup guidance missing: %v", event)
					}
				}
				if event["code"] != wantCode {
					t.Fatalf("incorrect Windows service error event: %v, want code %q", event, wantCode)
				}
			}
		})
	}
}

func TestSessionServiceFailuresOnlyPromptInstallationForExplicitActions(t *testing.T) {
	for _, operation := range []string{"start", "login", "login-new", "discover", "ping", "logout", "status", "unknown"} {
		t.Run(operation, func(t *testing.T) {
			if err := installationFailure(operation, nil); err != nil {
				t.Fatal("successful service resolution returned an error", err)
			}
			for _, failure := range []struct {
				name string
				err  error
			}{
				{"missing service", ErrNotInstalled},
				{"wrapped missing service", fmt.Errorf("probe: %w", ErrNotInstalled)},
				{"unresponsive service", errors.New("the background service is not responding")},
				{"protocol mismatch", errors.New("the app and installed service versions differ")},
				{"build mismatch", errors.New("update the TailSplit network service from this app before continuing")},
				{"canceled", fmt.Errorf("prepare: %w", context.Canceled)},
				{"deadline", fmt.Errorf("prepare: %w", context.DeadlineExceeded)},
			} {
				t.Run(failure.name, func(t *testing.T) {
					err := installationFailure(operation, failure.err)
					wantInstallation := operation == "start" || operation == "login" || operation == "login-new"
					wantInstallation = wantInstallation && !errors.Is(failure.err, context.Canceled) && !errors.Is(failure.err, context.DeadlineExceeded)
					if !errors.Is(err, failure.err) || errors.Is(err, ErrNeedsInstallation) != wantInstallation {
						t.Fatalf("service error lost its identity or category: %v", err)
					}
					if !wantInstallation && err != failure.err {
						t.Fatalf("ordinary service error was wrapped: %v", err)
					}
					wrapped := fmt.Errorf("%s: %w", operation, err)
					event := ErrorEvent(wrapped)
					wantCode := ""
					if wantInstallation {
						wantCode = "service-installation"
					}
					if event["code"] != wantCode || event["message"] != wrapped.Error() {
						t.Fatalf("incorrect service error event: %v, want code %q", event, wantCode)
					}
				})
			}
		})
	}
}
