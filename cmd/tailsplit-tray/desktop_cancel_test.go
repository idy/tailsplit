package main

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/idy/tailsplit/internal/tray"
)

func TestDesktopNativeOfferPollingRequiresPublishedLivePresentation(t *testing.T) {
	directory := t.TempDir()
	controller, err := tray.New(tray.Options{Helper: "unused", ConfigPath: filepath.Join(directory, "config.yaml"), LogPath: filepath.Join(directory, "core.log")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = controller.Close() })
	ready := make(chan struct{})
	var live atomic.Bool
	var predicates atomic.Int32
	observe := observeLiveNativeConnectRequests(controller, ready, func() bool { predicates.Add(1); return live.Load() })
	controller.RequestConnectionSetup()
	cold := controller.Snapshot().ConnectRequest
	observe(cold)
	if predicates.Load() != 0 {
		t.Fatal("observer read window state before pointer publication")
	}
	close(ready)
	observe(cold)
	if claimed, interrupted := controller.ClaimPanelConnectOperation(cold, cold, false); !claimed || interrupted {
		t.Fatal("pre-exposure native boot poll blocked cold fallback", claimed, interrupted)
	}
	live.Store(true)
	controller.OfferNativeConnectRequest(live.Load) // First live Show may have no pending request.
	controller.RequestConnectionSetup()
	visible := controller.Snapshot().ConnectRequest
	observe(visible)
	if claimed, interrupted := controller.ClaimPanelConnectOperation(visible, visible, false); claimed || !interrupted {
		t.Fatal("visible native polling did not reserve exact request", claimed, interrupted)
	}
	live.Store(false)
	controller.RequestConnectionSetup()
	fresh := controller.Snapshot().ConnectRequest
	observe(fresh)
	if claimed, interrupted := controller.ClaimPanelConnectOperation(fresh, fresh, false); !claimed || interrupted {
		t.Fatal("late dead-page poll blocked fresh explicit fallback", claimed, interrupted)
	}
}

func TestDesktopBoundCancelRevokesLoginOnEventLoopBeforeWorkerCompletion(t *testing.T) {
	directory := t.TempDir()
	controller, err := tray.New(tray.Options{Helper: "unused", ConfigPath: filepath.Join(directory, "config.yaml"), LogPath: filepath.Join(directory, "core.log")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = controller.Close() })
	controller.RequestConnectionSetup()
	request := controller.Snapshot().ConnectRequest
	if !controller.ClaimConnectOperation(request, "bound-login") {
		t.Fatal("claim failed")
	}
	ready, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	releaseCleanup := sync.OnceFunc(func() { close(release) })
	defer releaseCleanup()
	prepared := make(chan error, 1)
	go func() {
		prepared <- controller.ForOperation("bound-login").PrepareService(context.Background(), func(ctx context.Context) error {
			close(ready)
			<-ctx.Done()
			close(canceled)
			<-release
			return ctx.Err()
		})
	}()
	<-ready
	inbox := newDesktopLoginCancellations()
	callback := inbox.callback(controller)
	var flow tray.LoginFlow
	flow.RequestOperation("bound-login")
	result := make(chan error, 1)
	go func() { result <- callback(context.Background(), request, "stale-snapshot-operation") }()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("cancellation was blocked behind action worker")
	}
	select {
	case <-inbox.events():
	case <-time.After(time.Second):
		t.Fatal("host did not receive effective bound login target")
	}
	if !flow.ShowMessage() {
		t.Fatal("cancellation goroutine mutated event-loop-owned flow")
	}
	inbox.apply(&flow)
	if flow.ShowMessage() || flow.Next(tray.Snapshot{OperationID: "later-discovery", LoginURL: "https://login.tailscale.com/a/wrong-flow"}) != "" {
		t.Fatal("canceled login consumed later discovery authorization")
	}
	// Completion may arrive after revocation, before the next event-loop tick.
	if err := finishDesktopExitLogin(&flow, nil, "bound-login", "bound-login"); !errors.Is(err, tray.ErrOperationCanceled) {
		t.Fatal("late successful completion revived canceled browser intent", err)
	}
	flow.RequestOperation("newer-login")
	inbox.record([]string{"bound-login", "stale-snapshot-operation"})
	inbox.apply(&flow)
	if err := finishDesktopConnect(&flow, tray.ErrOperationCanceled, "bound-login"); !errors.Is(err, tray.ErrOperationCanceled) {
		t.Fatal(err)
	}
	if value := flow.Next(tray.Snapshot{OperationID: "newer-login", LoginURL: "https://login.tailscale.com/a/newer"}); value != "https://login.tailscale.com/a/newer" {
		t.Fatal("delayed old cancellation cleared newer login", value)
	}
	select {
	case err := <-result:
		t.Fatal("cancel acknowledged unfinished service cleanup", err)
	default:
	}
	releaseCleanup()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if err := <-prepared; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestDesktopCancellationInboxHandlesCompletionBeforeQueuedRevoke(t *testing.T) {
	inbox := newDesktopLoginCancellations()
	var flow tray.LoginFlow
	if err := finishDesktopConnect(&flow, nil, "old-login", "old-login"); err != nil {
		t.Fatal(err)
	}
	var senders sync.WaitGroup
	for range 32 {
		senders.Go(func() { inbox.record([]string{"old-login"}) })
	}
	senders.Wait()
	inbox.apply(&flow)
	if flow.Next(tray.Snapshot{OperationID: "later-discovery", LoginURL: "https://login.tailscale.com/a/discovery"}) != "" {
		t.Fatal("pending old browser intent survived queued cancellation")
	}
	flow.RequestOperation("new-login")
	inbox.record([]string{"old-login"})
	inbox.apply(&flow)
	if flow.Next(tray.Snapshot{OperationID: "new-login", LoginURL: "https://login.tailscale.com/a/new"}) == "" {
		t.Fatal("old cancellation affected new login")
	}
}

func TestDesktopReusedHelperLoginIsRevokedByItsOwnerWithoutAffectingNewHelper(t *testing.T) {
	var flow tray.LoginFlow
	flow.RequestOperation("sign-in-action", "existing-helper")
	if !flow.CancelOperation("existing-helper") || !flow.OperationCanceled("sign-in-action") {
		t.Fatal("helper cancellation did not revoke its reused browser intent")
	}
	if err := finishDesktopExitLogin(&flow, nil, "late-sign-in", "existing-helper"); !errors.Is(err, tray.ErrOperationCanceled) {
		t.Fatal("late reuse of canceled helper revived browser intent", err)
	}
	flow.RequestOperation("new-sign-in", "new-helper")
	flow.CancelOperation("existing-helper")
	if flow.Next(tray.Snapshot{OperationID: "new-helper", LoginURL: "https://login.tailscale.com/a/new-helper"}) == "" {
		t.Fatal("old helper cancellation cleared newer helper's login")
	}
}

func TestDesktopScopedLoginCannotUseAnotherHelpersAuthorization(t *testing.T) {
	var flow tray.LoginFlow
	flow.RequestOperation("old-login")
	if flow.Next(tray.Snapshot{OperationID: "new-discovery", LoginURL: "https://login.tailscale.com/a/discovery"}) != "" {
		t.Fatal("old pending login consumed a different helper's URL")
	}
	if !flow.OperationCanceled("old-login") {
		t.Fatal("replaced login remained replayable")
	}
}
