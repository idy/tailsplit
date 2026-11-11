package desktopwindow

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo"
)

func armNavigationURL(scope, request, operation string) string {
	return strings.Replace(cancellationNavigationURL(scope, request, operation), nativeCancelPath, nativeCancelArmPath, 1)
}

func TestNativeCancellationArmAcknowledgesStoredGuardWithoutStartingCancellation(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	var calls atomic.Int32
	inbox := newNativeCancellationInbox(ctx, func() nativeCancelHandler {
		return func(context.Context, string, string) error { calls.Add(1); return nil }
	})
	target := journalCancellation{Scope: "scope", Request: "request", OperationID: "owner"}
	var confirmations int
	inbox.armedAck = func(record journalCancellation) {
		inbox.mu.Lock()
		stored := inbox.armed[record]
		inbox.mu.Unlock()
		if record != target || !stored {
			t.Error("guard was acknowledged before its exact target was stored", record)
		}
		confirmations++
	}
	if !inbox.arm(target) || !inbox.arm(target) {
		t.Fatal("valid guard was rejected")
	}
	if calls.Load() != 0 || inbox.pendingRecovery() != nil || confirmations != 2 {
		t.Fatal("arm canceled before source loss or duplicate confirmation was lost", calls.Load(), confirmations)
	}
	inbox.mu.Lock()
	armed := len(inbox.armed)
	inbox.mu.Unlock()
	if armed != 1 {
		t.Fatal("duplicate arm created distinct targets", armed)
	}
}

func TestNativeCancellationArmCannotCrossScopePageOrSourceLife(t *testing.T) {
	frame, foreign := &mygo.Window{}, &mygo.Window{}
	page, child := &mygo.Page{}, &mygo.Page{}
	bindings := cancellationBindings{windows: make(map[*mygo.Window]cancellationBinding)}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	w := &Window{done: make(chan struct{}), journalScope: "scope", journalContext: ctx}
	bindings.register(frame, page, w)
	inbox := w.nativeCancellationInbox()
	var armedAcks atomic.Int32
	inbox.armedAck = func(journalCancellation) { armedAcks.Add(1) }
	valid := armNavigationURL("scope", "request", "owner")
	for _, raw := range []string{
		armNavigationURL("wrong", "request", "owner"),
		valid + "&request=duplicate", valid + "&scope=scope", valid + "&extra=unknown",
		armNavigationURL("scope", "", ""),
		armNavigationURL("scope", strings.Repeat("x", 1025), ""),
		strings.Replace(valid, navigationTestOrigin, "http://foreign.example", 1),
		strings.Replace(valid, nativeCancelArmPath, "/%6eative-cancel-arm", 1),
		strings.Replace(valid, nativeCancelArmPath, nativeCancelArmPath+"?", 1),
	} {
		event := &mygo.NavigateEvent{URL: raw}
		if bindings.cancellationNavigation(w, frame, page, navigationTestOrigin, navigationTestOrigin+"/", false, event) {
			t.Fatal("malformed or foreign guard was acknowledged")
		}
	}
	for _, caller := range []struct {
		frame     *mygo.Window
		page      *mygo.Page
		destroyed bool
	}{{foreign, page, false}, {frame, child, false}, {frame, page, true}} {
		event := &mygo.NavigateEvent{URL: valid}
		if bindings.cancellationNavigation(w, caller.frame, caller.page, navigationTestOrigin, navigationTestOrigin+"/", caller.destroyed, event) || !event.DefaultPrevented() {
			t.Fatal("unregistered/dead source armed guard or attempted network navigation")
		}
	}
	w.rendererUnavailable.Store(true)
	if bindings.cancellationNavigation(w, frame, page, navigationTestOrigin, navigationTestOrigin+"/", false, &mygo.NavigateEvent{URL: valid}) {
		t.Fatal("source death before arm admission was acknowledged")
	}
	w.promoteArmedCancellations()
	if armedAcks.Load() != 0 || w.pendingNativeCancellation() != nil {
		t.Fatal("unconfirmed guard created fail-safe cancellation", armedAcks.Load())
	}
}

func TestPrearmedCancellationSurvivesMissingCancelCallbackAndWaitsForKeeperRelease(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	frame, page := &mygo.Window{}, &mygo.Page{}
	bindings := cancellationBindings{windows: make(map[*mygo.Window]cancellationBinding)}
	closed, handlerStarted, allowAck, released := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	releaseHandler := sync.OnceFunc(func() { close(allowAck) })
	defer releaseHandler()
	var active atomic.Bool
	active.Store(true)
	var calls, armAcks, admittedAcks atomic.Int32
	w := &Window{done: make(chan struct{}), journalScope: "scope", journalContext: ctx, recoveryDispatch: func(fn func()) { fn() }, releaseJournalSession: func() {
		if active.Load() {
			t.Error("session released before promoted guard was acknowledged")
		}
		close(released)
	}}
	w.journal = &cancellationJournal{scope: "scope", list: func() ([]mygo.Cookie, error) { return nil, nil }}
	w.cancelHandler = func(delivery context.Context, request, operation string) error {
		calls.Add(1)
		if request != "claimed-request" || operation != "claimed-owner" {
			t.Error("promoted guard changed target", request, operation)
		}
		select {
		case <-closed:
		case <-delivery.Done():
			return delivery.Err()
		}
		close(handlerStarted)
		<-allowAck
		active.Store(false)
		return nil
	}
	bindings.register(frame, page, w)
	inbox := w.nativeCancellationInbox()
	inbox.armedAck = func(journalCancellation) { armAcks.Add(1) }
	inbox.admittedAck = func(journalCancellation) { admittedAcks.Add(1) }
	if !bindings.cancellationNavigation(w, frame, page, navigationTestOrigin, navigationTestOrigin+"/", false, &mygo.NavigateEvent{URL: armNavigationURL("scope", "claimed-request", "claimed-owner")}) {
		t.Fatal("guard was not admitted")
	}
	if armAcks.Load() != 1 || calls.Load() != 0 {
		t.Fatal("arm failed to acknowledge or canceled early", armAcks.Load(), calls.Load())
	}
	// The renderer dies before /native-cancel can reach a host callback. Only
	// its previously acknowledged exact arm may protect the pending operation.
	load := newPageLoad()
	w.load = load
	w.failLoad(load, false, errWebViewReadiness, func() { close(closed) })
	select {
	case <-handlerStarted:
	case <-time.After(time.Second):
		t.Fatal("source loss did not promote guard independently of stores")
	}
	if bindings.cancellationNavigation(w, frame, page, navigationTestOrigin, navigationTestOrigin+"/", true, &mygo.NavigateEvent{URL: cancellationNavigationURL("scope", "claimed-request", "claimed-owner")}) {
		t.Fatal("destroyed source admitted late cancellation callback")
	}
	if admittedAcks.Load() != 0 {
		t.Fatal("promoted guard fabricated a delivered cancellation navigation")
	}
	select {
	case <-released:
		t.Fatal("keeper released before promoted cleanup finished")
	default:
	}
	releaseHandler()
	select {
	case <-released:
	case <-time.After(5 * time.Second):
		t.Fatal("acknowledged promoted guard did not release keeper")
	}
	if active.Load() || calls.Load() != 1 || ctx.Err() != nil {
		t.Fatal("guard promotion lost exact delivery or root lifetime", calls.Load(), ctx.Err())
	}
}

func TestArmWithoutSourceLossDoesNotCancelOnNormalHideRecovery(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	var calls atomic.Int32
	w := &Window{done: make(chan struct{}), journalScope: "scope", journalContext: ctx, cancelHandler: func(context.Context, string, string) error { calls.Add(1); return nil }}
	w.journal = &cancellationJournal{scope: "scope", list: func() ([]mygo.Cookie, error) { return nil, nil }}
	target := journalCancellation{Scope: "scope", OperationID: "live-owner"}
	w.nativeCancellationInbox().arm(target)
	// OnClose prevents destruction, hides the page and invokes only this
	// recovery path. A surviving hidden renderer must not promote its guard.
	waitJournal(t, w.beginCancellationRecovery())
	inbox := w.nativeCancellationInbox()
	inbox.mu.Lock()
	armed := inbox.armed[target]
	inbox.mu.Unlock()
	if !armed || calls.Load() != 0 || w.pendingNativeCancellation() != nil {
		t.Fatal("normal hidden-page recovery promoted guard")
	}
}

func TestExplicitCancellationConsumesGuardAndAdmitsBeforeHandlerResult(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	started, allow := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(allow) })
	defer release()
	var calls atomic.Int32
	inbox := newNativeCancellationInbox(ctx, func() nativeCancelHandler {
		return func(context.Context, string, string) error { calls.Add(1); close(started); <-allow; return nil }
	})
	target := journalCancellation{Scope: "scope", Request: "request", OperationID: "operation"}
	inbox.arm(target)
	var admitted, completed atomic.Int32
	inbox.admittedAck = func(record journalCancellation) {
		inbox.mu.Lock()
		pending, armed := inbox.pending[record], inbox.armed[record]
		inbox.mu.Unlock()
		if record != target || !pending || armed {
			t.Error("admission ack preceded commit or left arm")
		}
		admitted.Add(1)
	}
	inbox.ack = func(record journalCancellation) {
		if record != target {
			t.Error("wrong complete ack")
		}
		completed.Add(1)
	}
	if !inbox.enqueue(target) || admitted.Load() != 1 {
		t.Fatal("explicit cancellation was not acknowledged at admission")
	}
	job := inbox.pendingRecovery()
	<-started
	if completed.Load() != 0 {
		t.Fatal("handler result was acknowledged before completion")
	}
	inbox.promoteArmed()
	release()
	waitJournal(t, job)
	// A delayed duplicate arm after completion must not recreate a fail-safe.
	inbox.arm(target)
	inbox.promoteArmed()
	if calls.Load() != 1 || inbox.pendingRecovery() != nil || completed.Load() != 2 {
		t.Fatal("completed exact target was canceled again", calls.Load(), completed.Load())
	}
}

func TestOldArmedTargetNeverRetargetsNewerOwner(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	current, active := "new-owner", true
	inbox := newNativeCancellationInbox(ctx, func() nativeCancelHandler {
		return func(_ context.Context, request, operation string) error {
			if request != "old-request" || operation != "old-owner" {
				t.Error("old guard guessed a newer target", request, operation)
			}
			if current == operation {
				active = false
			}
			return nil
		}
	})
	inbox.arm(journalCancellation{Scope: "scope", Request: "old-request", OperationID: "old-owner"})
	inbox.promoteArmed()
	if job := inbox.pendingRecovery(); job != nil {
		waitJournal(t, job)
	}
	if !active || current != "new-owner" {
		t.Fatal("old armed guard stopped unrelated new connection")
	}
}

func TestSourceLossBeforeAnyArmHasNoCancellationOrGuardConfirmation(t *testing.T) {
	var calls, confirmations atomic.Int32
	closed := make(chan struct{})
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	w := &Window{done: make(chan struct{}), journalScope: "scope", journalContext: ctx, recoveryDispatch: func(fn func()) { fn() }, cancelHandler: func(context.Context, string, string) error { calls.Add(1); return nil }}
	w.journal = &cancellationJournal{scope: "scope", list: func() ([]mygo.Cookie, error) { return nil, nil }}
	inbox := w.nativeCancellationInbox()
	inbox.armedAck = func(journalCancellation) { confirmations.Add(1) }
	load := newPageLoad()
	w.load = load
	w.failLoad(load, false, errWebViewReadiness, func() { close(closed) })
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("empty failed source was not released")
	}
	if calls.Load() != 0 || confirmations.Load() != 0 || w.pendingNativeCancellation() != nil {
		t.Fatal("source loss invented unarmed intent")
	}
	if !errors.Is(w.result(), errWebViewReadiness) {
		t.Fatal("failure result changed")
	}
}
