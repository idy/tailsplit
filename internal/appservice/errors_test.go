package appservice

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestServiceInstallationErrorsRetainDiagnosticsAndExposeRecovery(t *testing.T) {
	original := errors.New("update the TailSplit network service from this app before continuing")
	wrapped := fmt.Errorf("prepare login: %w", installationFailure(original))
	if !errors.Is(wrapped, original) || !errors.Is(wrapped, ErrNeedsInstallation) {
		t.Fatal("service error lost identity", wrapped)
	}
	event := ErrorEvent(wrapped)
	if event["code"] != "service-installation" || event["message"] != wrapped.Error() {
		t.Fatal("missing actionable error", event)
	}
	for _, err := range []error{context.Canceled, context.DeadlineExceeded, errors.New("device authorization failed")} {
		classified := err
		if err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
			classified = installationFailure(err)
		}
		if ErrorEvent(classified)["code"] != "" {
			t.Fatal("unrelated error prompted installation", err)
		}
	}
}
