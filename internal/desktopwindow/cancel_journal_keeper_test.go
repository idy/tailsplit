package desktopwindow

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo"
)

func TestJournalKeeperFreezesRendererAndDrainsNewIntentBeforeSessionRelease(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	first := journalCookie(t, "scope", "first", "request-first", "owner-first")
	second := journalCookie(t, "scope", "second", "request-second", "owner-second")
	var mu sync.Mutex
	store := map[string]mygo.Cookie{first.Name: first}
	sourceDestroyed, sessionReleased := make(chan struct{}), make(chan struct{})
	var targets []string
	w := &Window{done: make(chan struct{}), journalContext: ctx, recoveryDispatch: func(fn func()) { fn() }}
	w.journal = &cancellationJournal{scope: "scope", list: func() ([]mygo.Cookie, error) {
		select {
		case <-sourceDestroyed:
		default:
			t.Error("journal read raced a still-running source renderer")
		}
		mu.Lock()
		defer mu.Unlock()
		var cookies []mygo.Cookie
		for _, cookie := range store {
			cookies = append(cookies, cookie)
		}
		return cookies, nil
	}, remove: func(cookie mygo.Cookie) error {
		mu.Lock()
		defer mu.Unlock()
		delete(store, cookie.Name)
		return nil
	}}
	w.cancelHandler = func(_ context.Context, _, operation string) error {
		mu.Lock()
		defer mu.Unlock()
		targets = append(targets, operation)
		if operation == "owner-first" {
			// A distinct journal entry becomes visible while the first cleanup
			// acknowledges. Success requires a fresh read, not just deletion A.
			store[second.Name] = second
		}
		return nil
	}
	w.releaseJournalSession = func() {
		mu.Lock()
		defer mu.Unlock()
		if len(store) != 0 || !reflect.DeepEqual(targets, []string{"owner-first", "owner-second"}) {
			t.Error("session release discarded a committed cancellation", targets)
		}
		close(sessionReleased)
	}
	load := newPageLoad()
	w.load = load
	w.failLoad(load, false, errWebViewReadiness, func() {
		if !w.isDone() || !errors.Is(w.result(), errWebViewReadiness) {
			t.Error("renderer destroyed before failure publication")
		}
		close(sourceDestroyed)
	})
	select {
	case <-sessionReleased:
	case <-time.After(5 * time.Second):
		t.Fatal("drained journal did not release its native keeper")
	}
}

func TestJournalSessionReleaseJoinsNewerRecoveryInsteadOfStaleCompletedJob(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	ui := make(chan func(), 4)
	sessionReleased := make(chan struct{})
	var mu sync.Mutex
	var cookies []mygo.Cookie
	blockRead, readStarted := make(chan struct{}), make(chan struct{})
	releaseRead := sync.OnceFunc(func() { close(blockRead) })
	defer releaseRead()
	var once sync.Once
	journal := &cancellationJournal{scope: "scope", list: func() ([]mygo.Cookie, error) {
		mu.Lock()
		values := append([]mygo.Cookie(nil), cookies...)
		mu.Unlock()
		if len(values) > 0 {
			once.Do(func() { close(readStarted) })
			<-blockRead
		}
		return values, nil
	}, remove: func(mygo.Cookie) error { mu.Lock(); cookies = nil; mu.Unlock(); return nil }}
	cancel := func(context.Context, string, string) error { return nil }
	first := journal.recover(ctx, cancel)
	waitJournal(t, first)
	w := &Window{done: make(chan struct{}), journal: journal, recoveryDispatch: func(fn func()) { ui <- fn }, releaseJournalSession: func() { close(sessionReleased) }}
	w.complete(errWebViewReadiness)
	w.releaseJournalAfterRecovery(first)
	var staleRelease func()
	select {
	case staleRelease = <-ui:
	case <-time.After(5 * time.Second):
		t.Fatal("release was not queued on UI loop")
	}
	mu.Lock()
	cookies = []mygo.Cookie{journalCookie(t, "scope", "newer", "", "newer-owner")}
	mu.Unlock()
	newer := journal.recover(ctx, cancel)
	<-readStarted
	staleRelease()
	select {
	case <-sessionReleased:
		t.Fatal("old completion discarded newer recovery")
	default:
	}
	releaseRead()
	waitJournal(t, newer)
	select {
	case release := <-ui:
		release()
	case <-time.After(5 * time.Second):
		t.Fatal("newer recovery did not queue release")
	}
	select {
	case <-sessionReleased:
	default:
		t.Fatal("latest acknowledged recovery retained session forever")
	}
}

func TestAcknowledgedCookieDeletionFailureDoesNotReplayForever(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	cookie := journalCookie(t, "scope", "entry", "", "owner")
	calls, reads := 0, 0
	journal := &cancellationJournal{scope: "scope", list: func() ([]mygo.Cookie, error) { reads++; return []mygo.Cookie{cookie}, nil }, remove: func(mygo.Cookie) error { return errors.New("cookie deletion unavailable") }}
	waitJournal(t, journal.recover(ctx, func(context.Context, string, string) error { calls++; return nil }))
	if calls != 1 || reads < 2 {
		t.Fatal("acknowledged delivery was not drained exactly once", calls, reads)
	}
}
