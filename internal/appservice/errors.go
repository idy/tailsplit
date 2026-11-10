package appservice

import (
	"context"
	"errors"
	"fmt"
)

var ErrNeedsInstallation = errors.New("network service installation or update is required")
var ErrServiceSetupRequired = errors.New("the Windows network service is unavailable; rerun TailSplit Setup to install or repair it")

type installationError struct{ error }

func (e installationError) Unwrap() error        { return e.error }
func (e installationError) Is(target error) bool { return target == ErrNeedsInstallation }

// Only explicit connection or sign-in failures offer installation recovery.
func installationFailure(operation string, err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	switch operation {
	case "start", "login", "login-new":
		return installationError{err}
	default:
		return err
	}
}

// Windows service repair requires rerunning Setup; retry cannot open Installer.
func windowsServiceFailure(operation string, err error) error {
	if !errors.Is(err, ErrNotInstalled) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	switch operation {
	case "start", "login", "login-new":
		return fmt.Errorf("%w: %w", ErrServiceSetupRequired, err)
	default:
		return err
	}
}

// ErrorEvent carries an actionable category across the private helper channel.
// The desktop never needs to guess recovery actions from translated diagnostics.
func ErrorEvent(err error) map[string]string {
	event := map[string]string{"event": "error", "message": err.Error()}
	if errors.Is(err, ErrServiceSetupRequired) {
		event["code"] = "service-setup-required"
	} else if errors.Is(err, ErrNeedsInstallation) {
		event["code"] = "service-installation"
	}
	return event
}
