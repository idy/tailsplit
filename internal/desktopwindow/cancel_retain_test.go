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

func TestRetainCancellationOwnsIntentBeforeReceiptAndSurvivesJournalEviction(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	frame, page := &mygo.Window{}, &mygo.Page{}
	bindings := cancellationBindings{windows: make(map[*mygo.Window]cancellationBinding)}
	sourceClosed, started, allowFinish, released := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(allowFinish) })
	defer release()
	var active atomic.Bool
	active.Store(true)
	var calls atomic.Int32
	w := &Window{done: make(chan struct{}), journalScope: "scope", journalContext: ctx, recoveryDispatch: func(fn func()) { fn() }, releaseJournalSession: func() { close(released) }}
	w.journal = &cancellationJournal{scope: "scope", list: func() ([]mygo.Cookie, error) { return nil, nil }} // All renderer records evicted.
	w.cancelHandler = func(delivery context.Context, request, operation string) error {
		calls.Add(1)
		if request != "request" || operation != "owner" {
			t.Error("retained target changed", request, operation)
		}
		close(started)
		select {
		case <-sourceClosed:
		case <-delivery.Done():
			return delivery.Err()
		}
		select {
		case <-allowFinish:
		case <-delivery.Done():
			return delivery.Err()
		}
		active.Store(false)
		return nil
	}
	bindings.register(frame, page, w)
	receipt, err := bindings.retainFrom(frame, page, "scope", "request", "owner")
	if err != nil || receipt != (journalCancellation{Scope: "scope", Request: "request", OperationID: "owner"}) {
		t.Fatal("host did not acknowledge exact retention", receipt, err)
	}
	inbox := w.nativeCancellationInbox()
	inbox.mu.Lock()
	retained := inbox.pending[receipt]
	inbox.mu.Unlock()
	if !retained {
		t.Fatal("receipt preceded host ownership")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("host worker did not receive retained intent")
	}
	load := newPageLoad()
	w.load = load
	w.failLoad(load, false, errWebViewReadiness, func() { close(sourceClosed) })
	select {
	case <-released:
		t.Fatal("keeper released before retained cancellation completed")
	default:
	}
	release()
	select {
	case <-released:
	case <-time.After(5 * time.Second):
		t.Fatal("retained cancellation did not finish after source loss")
	}
	if active.Load() || calls.Load() != 1 {
		t.Fatal("lost or repeated retained cancellation", active.Load(), calls.Load())
	}
}

func TestRetainCancellationValidatesBindingScopeAndTargetBeforeAcknowledging(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	frame, foreign := &mygo.Window{}, &mygo.Window{}
	page, child := &mygo.Page{}, &mygo.Page{}
	bindings := cancellationBindings{windows: make(map[*mygo.Window]cancellationBinding)}
	w := &Window{journalScope: "scope", journalContext: ctx}
	bindings.register(frame, page, w)
	for _, input := range []struct {
		frame                     *mygo.Window
		page                      *mygo.Page
		scope, request, operation string
	}{
		{foreign, page, "scope", "request", "owner"}, {frame, child, "scope", "request", "owner"}, {frame, nil, "scope", "request", "owner"},
		{frame, page, "foreign", "request", "owner"}, {frame, page, "", "request", "owner"}, {frame, page, "scope", "", ""},
		{frame, page, "scope", strings.Repeat("x", 1025), ""},
	} {
		receipt, err := bindings.retainFrom(input.frame, input.page, input.scope, input.request, input.operation)
		if err == nil || receipt != (journalCancellation{}) {
			t.Fatal("invalid retention acknowledged", receipt, err)
		}
	}
	if w.pendingNativeCancellation() != nil {
		t.Fatal("rejected retention created work")
	}
	stop()
	if _, err := bindings.retainFrom(frame, page, "scope", "request", "owner"); !errors.Is(err, ErrNotReady) {
		t.Fatal("stopped host acknowledged retention", err)
	}
}
