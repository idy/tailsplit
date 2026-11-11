package desktopwindow

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo"
)

func journalCookie(t *testing.T, scope, entry, request, operation string) mygo.Cookie {
	t.Helper()
	data, err := json.Marshal(journalCancellation{Scope: scope, Request: request, OperationID: operation})
	if err != nil {
		t.Fatal(err)
	}
	return mygo.Cookie{Name: cancelJournalPrefix + scope + "-" + entry, Value: url.PathEscape(string(data)), Path: "/"}
}

func waitJournal(t *testing.T, job *journalRecovery) {
	t.Helper()
	select {
	case <-job.done:
		if job.err != nil {
			t.Fatal(job.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("native cancellation recovery did not finish")
	}
}

func TestCancellationJournalURLPreservesCapabilityAndOrigin(t *testing.T) {
	const raw = "http://127.0.0.1:43821/#token=fixture&connect=request&other=retained"
	updated, scope := cancellationJournalURL(raw)
	parsed, err := url.Parse(updated)
	if err != nil || scope == "" || parsed.Scheme != "http" || parsed.Host != "127.0.0.1:43821" || parsed.Path != "/" || parsed.RawQuery != "" {
		t.Fatal("journal changed the native panel origin/path", err)
	}
	fragment, _ := url.ParseQuery(parsed.Fragment)
	if fragment.Get("token") != "fixture" || fragment.Get("connect") != "request" || fragment.Get("other") != "retained" || fragment.Get("journal") != scope {
		t.Fatal("journal lost a native fragment field")
	}
}

func TestClaimedNativeOwnerCancellationRecoversBeforeSessionDestruction(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	cookie := journalCookie(t, "window-scope", "entry", "", "already-claimed-owner")
	var mu sync.Mutex
	active := true
	removed := false
	destroyed := make(chan struct{})
	w := &Window{done: make(chan struct{}), journalContext: ctx, recoveryDispatch: func(fn func()) { fn() }}
	w.visible.Store(true)
	w.cancelHandler = func(_ context.Context, request, operation string) error {
		mu.Lock()
		defer mu.Unlock()
		if request != "" || operation != "already-claimed-owner" {
			t.Error("recovery guessed another operation", request, operation)
		}
		active = false // The exact-ID controller callback, independent of renderer/HTTP.
		return nil
	}
	w.journal = &cancellationJournal{scope: "window-scope", list: func() ([]mygo.Cookie, error) {
		select {
		case <-destroyed:
			t.Error("ephemeral session was destroyed before its journal was read")
		default:
		}
		return []mygo.Cookie{cookie}, nil
	}, remove: func(value mygo.Cookie) error {
		mu.Lock()
		defer mu.Unlock()
		if active || value.Name != cookie.Name {
			t.Error("journal was cleared before guarded cancellation acknowledgment")
		}
		removed = true
		return nil
	}}
	load := newPageLoad()
	w.load = load
	w.failLoad(load, false, errWebViewReadiness, func() {
		mu.Lock()
		defer mu.Unlock()
		if active || !removed || !w.isDone() || !errors.Is(w.result(), errWebViewReadiness) {
			t.Error("fallback destroyed its only recovery copy or lost the original failure")
		}
		close(destroyed)
	})
	select {
	case <-destroyed:
	case <-time.After(5 * time.Second):
		t.Fatal("recovery did not hand off the failed native window")
	}
	if !w.WantsVisible() || w.CanPresentConnectRequest() {
		t.Fatal("recovery lost fallback intent or kept accepting native requests")
	}
}

func TestSlowJournalRecoveryKeepsEphemeralSessionAcrossBrowserHandoff(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	cookie := journalCookie(t, "scope", "entry", "claimed-native-request", "claimed-owner")
	readStarted, releaseRead := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(releaseRead) })
	defer release()
	var once sync.Once
	destroyed, canceled := make(chan struct{}), make(chan struct{})
	w := &Window{done: make(chan struct{}), journalContext: ctx, recoveryDispatch: func(fn func()) { fn() }}
	w.cancelHandler = func(context.Context, string, string) error { close(canceled); return nil }
	w.journal = &cancellationJournal{scope: "scope", list: func() ([]mygo.Cookie, error) {
		once.Do(func() { close(readStarted) })
		<-releaseRead
		return []mygo.Cookie{cookie}, nil
	}, remove: func(mygo.Cookie) error { return nil }}
	load := newPageLoad()
	w.load = load
	w.failLoad(load, false, errWebViewReadiness, func() { close(destroyed) })
	<-readStarted
	select {
	case <-w.done: // Browser fallback can open while the profile remains alive.
	case <-time.After(5 * time.Second):
		t.Fatal("a slow store blocked browser fallback")
	}
	select {
	case <-destroyed:
		t.Fatal("handoff discarded the only committed cancellation record")
	default:
	}
	release()
	select {
	case <-destroyed:
	case <-time.After(5 * time.Second):
		t.Fatal("acknowledged recovery did not release the retained native session")
	}
	select {
	case <-canceled:
	default:
		t.Fatal("native session was released before replay")
	}
}

func TestJournalFailureAloneNeverCancelsAnOperationWithoutAnIntent(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	calls := 0
	journal := &cancellationJournal{scope: "scope", list: func() ([]mygo.Cookie, error) { return nil, nil }, remove: func(mygo.Cookie) error { t.Error("removed nonexistent intent"); return nil }}
	waitJournal(t, journal.recover(ctx, func(context.Context, string, string) error { calls++; return nil }))
	if calls != 0 {
		t.Fatal("renderer failure guessed that a running connection should stop")
	}
}

func TestJournalRecoveryIgnoresForeignAndMalformedRecords(t *testing.T) {
	valid := journalCookie(t, "scope", "valid", "", "owner")
	foreign := journalCookie(t, "another-scope", "other", "request", "other-owner")
	wrongScope := journalCookie(t, "scope", "wrong", "request", "wrong-owner")
	wrongScope.Value = strings.ReplaceAll(wrongScope.Value, "scope", "wrong")
	unknown := valid
	unknown.Name += "unknown"
	unknown.Value = url.PathEscape(`{"scope":"scope","request":"","operation_id":"owner","token":"forbidden"}`)
	trailing := valid
	trailing.Name += "trailing"
	trailing.Value += "%7B%7D"
	empty := journalCookie(t, "scope", "empty", "", "")
	oversized := journalCookie(t, "scope", "oversized", "", strings.Repeat("x", 1025))
	cookies := []mygo.Cookie{foreign, wrongScope, unknown, trailing, empty, oversized, {Name: "unrelated", Value: "not-a-cancellation"}, valid}
	journal := &cancellationJournal{scope: "scope", list: func() ([]mygo.Cookie, error) { return cookies, nil }}
	var removed []string
	journal.remove = func(cookie mygo.Cookie) error { removed = append(removed, cookie.Name); return nil }
	var targets []string
	if err := journal.replay(func(_ context.Context, request, operation string) error {
		targets = append(targets, request+"/"+operation)
		return nil
	}, context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(targets, []string{"/owner"}) || !reflect.DeepEqual(removed, []string{valid.Name}) {
		t.Fatal("foreign or malformed state changed cancellation", targets, removed)
	}
}

func TestRecoveryJoiningAnEarlierReadRescansAfterTheNewFailure(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	cookie := journalCookie(t, "scope", "late", "", "last-committed-owner")
	readStarted, releaseRead := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(releaseRead) })
	defer release()
	var mu sync.Mutex
	reads, recovered := 0, false
	journal := &cancellationJournal{scope: "scope", list: func() ([]mygo.Cookie, error) {
		mu.Lock()
		reads++
		first := reads == 1
		mu.Unlock()
		if first {
			close(readStarted)
			<-releaseRead
			return nil, nil // Snapshot predates the last committed cancellation.
		}
		return []mygo.Cookie{cookie}, nil
	}, remove: func(mygo.Cookie) error { return nil }}
	cancel := func(context.Context, string, string) error { recovered = true; return nil }
	first := journal.recover(ctx, cancel)
	<-readStarted
	joined := journal.recover(ctx, cancel)
	if first != joined {
		t.Fatal("renderer failures spawned duplicate store readers")
	}
	release()
	waitJournal(t, joined)
	if !recovered || reads < 2 {
		t.Fatal("a pre-failure read discarded the last committed cancellation", reads)
	}
}

func TestJournalRetriesDeliveryWithoutDiscardingUnacknowledgedIntent(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	cookie := journalCookie(t, "scope", "entry", "", "owner")
	var mu sync.Mutex
	pending, attempts := true, 0
	firstFailure := make(chan struct{})
	allowRetry := make(chan struct{})
	releaseRetry := sync.OnceFunc(func() { close(allowRetry) })
	defer releaseRetry()
	journal := &cancellationJournal{scope: "scope", list: func() ([]mygo.Cookie, error) {
		mu.Lock()
		defer mu.Unlock()
		if !pending {
			return nil, nil
		}
		return []mygo.Cookie{cookie}, nil
	}, remove: func(mygo.Cookie) error {
		mu.Lock()
		defer mu.Unlock()
		if attempts < 2 {
			t.Error("failed delivery discarded intent")
		}
		pending = false
		return nil
	}}
	job := journal.recover(ctx, func(context.Context, string, string) error {
		mu.Lock()
		attempts++
		first := attempts == 1
		mu.Unlock()
		if first {
			close(firstFailure)
			return errors.New("cleanup still in progress")
		}
		<-allowRetry
		return nil
	})
	<-firstFailure
	mu.Lock()
	preserved := pending
	mu.Unlock()
	if !preserved {
		t.Fatal("recovery lost its only intent after a failed delivery")
	}
	releaseRetry()
	waitJournal(t, job)
	mu.Lock()
	defer mu.Unlock()
	if pending || attempts != 2 {
		t.Fatal("recovery failed to retry exact target", attempts, pending)
	}
}

func TestRecoveredOldJournalLeavesANewerOwnerRunning(t *testing.T) {
	cookie := journalCookie(t, "scope", "entry", "old-request", "old-operation")
	journal := &cancellationJournal{scope: "scope", list: func() ([]mygo.Cookie, error) { return []mygo.Cookie{cookie}, nil }, remove: func(mygo.Cookie) error { return nil }}
	current, active := "new-operation", true
	if err := journal.replay(func(_ context.Context, request, operation string) error {
		if request != "old-request" || operation != "old-operation" {
			t.Fatal("journal retargeted cancellation")
		}
		if current == operation {
			active = false
		}
		return nil
	}, context.Background()); err != nil {
		t.Fatal(err)
	}
	if !active || current != "new-operation" {
		t.Fatal("old renderer recovery disconnected a newer owner")
	}
}

func TestOldCookieAcknowledgmentCannotEraseANewerCancellation(t *testing.T) {
	old := journalCookie(t, "scope", "old-entry", "request-old", "owner-old")
	newer := journalCookie(t, "scope", "new-entry", "request-new", "owner-new")
	store := map[string]mygo.Cookie{old.Name: old}
	journal := &cancellationJournal{scope: "scope", list: func() ([]mygo.Cookie, error) { return []mygo.Cookie{old}, nil }, remove: func(cookie mygo.Cookie) error { delete(store, cookie.Name); return nil }}
	if err := journal.replay(func(context.Context, string, string) error { store[newer.Name] = newer; return nil }, context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store) != 1 || store[newer.Name].Name != newer.Name {
		t.Fatal("old acknowledgment erased a newer cancellation")
	}
}
