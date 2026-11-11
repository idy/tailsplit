package tray

import (
	"errors"
	"testing"
)

func TestLocalBrowserCancelWinsOverURLRefreshWithoutRevokingHelper(t *testing.T) {
	var flow LoginFlow
	flow.RequestOperation("helper-owner", "helper-owner")
	old := Snapshot{OperationID: "helper-owner", LoginURL: "https://login.tailscale.com/a/old", BackendState: "NeedsLogin"}
	if flow.NextBrowserRequest(old).URL == "" {
		t.Fatal("fixture did not select URL")
	}
	flow.Cancel() // Native settings/apply abandons browser intent, not its owner.
	flow.Complete(ErrLoginURLChanged)
	latest := old
	latest.LoginURL = "https://login.tailscale.com/a/latest"
	if flow.NextBrowserRequest(latest).URL != "" || flow.ShowMessage() {
		t.Fatal("URL refresh revived an explicitly abandoned browser intent")
	}
	if flow.OperationCanceled("helper-owner") || !flow.RequestOperation("fresh-intent", "helper-owner") {
		t.Fatal("abandoning a browser intent poisoned its still-live helper")
	}
}

func TestBrowserTransitionRejectionDoesNotRevokeAnUncanceledLiveOwner(t *testing.T) {
	controller, initial := loginBrowserStartFixture(t)
	var flow LoginFlow
	flow.RequestOperation(initial.OwnerID, initial.OwnerID)
	request := flow.NextBrowserRequest(controller.Snapshot())
	controller.mu.Lock()
	controller.reconfiguring = true
	controller.mu.Unlock()
	defer func() { controller.mu.Lock(); controller.reconfiguring = false; controller.mu.Unlock() }()
	err := controller.AuthorizeLoginBrowserStart(request, func() error { t.Error("transition launched a browser"); return nil })
	if !errors.Is(err, ErrOperationCanceled) {
		t.Fatal("transition was not rejected", err)
	}
	if err := controller.ForOperation(initial.OwnerID).Check(); err != nil {
		t.Fatal("fixture actually canceled its owner", err)
	}
	flow.Cancel()
	flow.Complete(err)
	controller.mu.Lock()
	controller.reconfiguring = false
	controller.mu.Unlock()
	if !flow.RequestOperation("fresh-explicit-signin", initial.OwnerID) {
		t.Fatal("browser-result rejection permanently revoked an active uncanceled owner")
	}
}
