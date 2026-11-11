package desktopwindow

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/idy/tailsplit/internal/tray"
)

func TestEstablishedGlobalOwnerSurvivesWindowFailureWithoutExplicitCancel(t *testing.T) {
	for _, retiredURL := range []bool{false, true} {
		t.Run(map[bool]string{false: "no-cancel-intent", true: "retired-arm-cannot-create-intent"}[retiredURL], func(t *testing.T) {
			ctx, stop := context.WithCancel(context.Background())
			defer stop()
			var canceled atomic.Bool
			var calls atomic.Int32
			current := tray.Snapshot{State: "running", Active: true, Mode: "tun", RoutingMode: "global", OperationID: "established-global-owner"}
			released, sourceClosed := make(chan struct{}), make(chan struct{})
			w := &Window{done: make(chan struct{}), journalScope: "scope", journalContext: ctx, recoveryDispatch: func(fn func()) { fn() }, releaseJournalSession: func() { close(released) }, cancelHandler: func(_ context.Context, _, operation string) error {
				calls.Add(1)
				if operation == current.OperationID {
					canceled.Store(true)
				}
				return nil
			}}
			w.journal = &cancellationJournal{scope: "scope", list: func() ([]mygo.Cookie, error) { return nil, nil }}
			// Rendering creates the explicit inbox and callbacks, but never an
			// armed target or a running cancellation worker.
			w.nativeCancellationInbox()
			if retiredURL {
				frame, page := &mygo.Window{}, &mygo.Page{}
				bindings := cancellationBindings{windows: make(map[*mygo.Window]cancellationBinding)}
				bindings.register(frame, page, w)
				raw := strings.Replace(cancellationNavigationURL("scope", "", current.OperationID), nativeCancelPath, "/native-cancel-arm", 1)
				event := &mygo.NavigateEvent{URL: raw}
				if bindings.cancellationNavigation(w, frame, page, navigationTestOrigin, navigationTestOrigin+"/", false, event) {
					t.Fatal("retired arm route created cancellation work")
				}
				policy := &recordedPagePolicy{}
				configurePagePolicy(policy, 43821)
				policy.navigate(event)
				if !event.DefaultPrevented() {
					t.Fatal("ordinary navigation policy allowed retired arm route")
				}
			}
			load := newPageLoad()
			w.load = load
			w.failLoad(load, false, errWebViewReadiness, func() { close(sourceClosed) })
			select {
			case <-released:
			case <-time.After(time.Second):
				t.Fatal("no-intent source failure retained session forever")
			}
			select {
			case <-sourceClosed:
			default:
				t.Fatal("failed source was not released")
			}
			if calls.Load() != 0 || canceled.Load() || w.pendingNativeCancellation() != nil || ctx.Err() != nil || !current.Active || current.RoutingMode != "global" {
				t.Fatal("view failure altered established Global connection", calls.Load(), canceled.Load(), current, ctx.Err())
			}
		})
	}
}

func TestExplicitCancellationAdmitsBeforeResultAndDeduplicatesCompletedTarget(t *testing.T) {
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
	var admitted, completed atomic.Int32
	inbox.admittedAck = func(record journalCancellation) {
		inbox.mu.Lock()
		pending, done := inbox.pending[record], inbox.acknowledged[record]
		inbox.mu.Unlock()
		if record != target || !pending && !done {
			t.Error("admission ack preceded exact inbox commit")
		}
		admitted.Add(1)
	}
	inbox.ack = func(record journalCancellation) {
		if record != target {
			t.Error("wrong completion ack")
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
	release()
	waitJournal(t, job)
	if !inbox.enqueue(target) {
		t.Fatal("completed target lost idempotent acknowledgment")
	}
	if calls.Load() != 1 || inbox.pendingRecovery() != nil || admitted.Load() != 2 || completed.Load() != 2 {
		t.Fatal("completed explicit target was re-delivered", calls.Load(), admitted.Load(), completed.Load())
	}
}

func TestDerivedSourceURLRemovesRetiredGuardMetadata(t *testing.T) {
	source, err := journalSourceURL("http://127.0.0.1:43821/#token=fixture&journal=scope&cancel_guard=1", true)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	fragment, err := url.ParseQuery(parsed.Fragment)
	if err != nil || fragment.Has("cancel_guard") || fragment.Get("cancel_host") != "1" || fragment.Get("journal_backup") != "1" || fragment.Get("journal") != "scope" || fragment.Get("token") != "fixture" {
		t.Fatal("source still advertised retired source-loss guard", source, err)
	}
}
