package appservice

import (
	"context"
	"errors"
)

var ErrNeedsInstallation = errors.New("network service installation or update is required")

type installationError struct{ error }

func (e installationError) Unwrap() error        { return e.error }
func (e installationError) Is(target error) bool { return target == ErrNeedsInstallation }

func installationFailure(err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return installationError{err}
}

// ErrorEvent carries an actionable category across the private helper channel.
// The desktop never needs to guess recovery actions from translated diagnostics.
func ErrorEvent(err error) map[string]string {
	event := map[string]string{"event": "error", "message": err.Error()}
	if errors.Is(err, ErrNeedsInstallation) || errors.Is(err, ErrNotInstalled) {
		event["code"] = "service-installation"
	}
	return event
}
