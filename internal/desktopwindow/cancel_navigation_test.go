package desktopwindow

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo"
)

const navigationTestOrigin = "http://127.0.0.1:43821"

func cancellationNavigationURL(scope, request, operation string) string {
	fields := url.Values{"scope": {scope}, "request": {request}, "operation_id": {operation}}
	return navigationTestOrigin + nativeCancelPath + "#" + fields.Encode()
}

func TestNativeCancellationNavigationStrictExactTarget(t *testing.T) {
	request, operation := "request&operation_id=not-a-field", "operation+percent%26value"
	raw := cancellationNavigationURL("scope", request, operation)
	record, valid := parseNativeCancellation(navigationTestOrigin, "scope", raw)
	if !valid || record.Scope != "scope" || record.Request != request || record.OperationID != operation {
		t.Fatal("fragment decoding changed the exact target", record, valid)
	}
	for _, bad := range []string{
		cancellationNavigationURL("wrong", "request", "operation"),
		cancellationNavigationURL("scope", "", ""),
		cancellationNavigationURL("scope", strings.Repeat("x", 1025), ""),
		cancellationNavigationURL("scope", strings.Repeat("界", 342), ""),
		raw + "&request=duplicate", raw + "&scope=scope", raw + "&token=forbidden",
		strings.Replace(raw, navigationTestOrigin, "https://127.0.0.1:43821", 1),
		strings.Replace(raw, navigationTestOrigin, "http://127.0.0.1:43822", 1),
		strings.Replace(raw, navigationTestOrigin, "http://user@127.0.0.1:43821", 1),
		strings.Replace(raw, nativeCancelPath, "/%6eative-cancel", 1),
		strings.Replace(raw, nativeCancelPath, nativeCancelPath+"?", 1),
		strings.Replace(raw, nativeCancelPath, nativeCancelPath+"?request=other", 1),
		navigationTestOrigin + nativeCancelPath + "#scope=scope&request=%&operation_id=operation",
	} {
		if value, ok := parseNativeCancellation(navigationTestOrigin, "scope", bad); ok {
			t.Fatal("accepted malformed cancellation navigation", value)
		}
	}
	if _, ok := parseNativeCancellation(navigationTestOrigin, "scope", cancellationNavigationURL("scope", strings.Repeat("x", 1024), "")); !ok {
		t.Fatal("rejected valid maximum target")
	}
	defaultPort := strings.Replace(cancellationNavigationURL("scope", "request", "operation"), navigationTestOrigin, "http://127.0.0.1", 1)
	if _, ok := parseNativeCancellation("http://127.0.0.1:80", "scope", defaultPort); !ok {
		t.Fatal("WebKit default-port normalization changed origin identity")
	}
}

func TestNativeCancellationNavigationOnlyAcceptsRegisteredMainPage(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	frame, foreignFrame := &mygo.Window{}, &mygo.Window{}
	page, child, keeper := &mygo.Page{}, &mygo.Page{}, &mygo.Page{}
	bindings := cancellationBindings{windows: make(map[*mygo.Window]cancellationBinding)}
	var calls atomic.Int32
	w := &Window{done: make(chan struct{}), journalScope: "scope", journalContext: ctx, cancelHandler: func(context.Context, string, string) error { calls.Add(1); return nil }}
	bindings.register(frame, page, w)
	for _, test := range []struct {
		frame     *mygo.Window
		page      *mygo.Page
		source    string
		destroyed bool
	}{
		{foreignFrame, page, navigationTestOrigin + "/", false},
		{frame, child, navigationTestOrigin + "/", false},
		{frame, keeper, navigationTestOrigin + journalReaderPath, false},
		{frame, page, "https://foreign.example/", false},
		{frame, page, navigationTestOrigin + journalReaderPath, false},
		{frame, page, navigationTestOrigin + "/", true},
	} {
		event := &mygo.NavigateEvent{URL: cancellationNavigationURL("scope", "request", "operation")}
		if bindings.cancellationNavigation(w, test.frame, test.page, navigationTestOrigin, test.source, test.destroyed, event) || !event.DefaultPrevented() {
			t.Fatal("unregistered source reached navigation inbox or attempted HTTP load")
		}
	}
	if calls.Load() != 0 || w.pendingNativeCancellation() != nil {
		t.Fatal("rejected navigation created cancellation work")
	}
	event := &mygo.NavigateEvent{URL: cancellationNavigationURL("scope", "request", "operation")}
	if !bindings.cancellationNavigation(w, frame, page, navigationTestOrigin, navigationTestOrigin+"/", false, event) || !event.DefaultPrevented() {
		t.Fatal("valid main navigation was not synchronously admitted/prevented")
	}
	if job := w.pendingNativeCancellation(); job != nil {
		waitJournal(t, job)
	}
	if calls.Load() != 1 {
		t.Fatal("accepted navigation was not delivered exactly", calls.Load())
	}
}

func TestNavigationCancellationSurvivesSourceDestructionWithoutAnyStoreOrLegacyDelivery(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	sourceClosed, cleanupStarted, allowAck, released := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	releaseCleanup := sync.OnceFunc(func() { close(allowAck) })
	defer releaseCleanup()
	frame, page := &mygo.Window{}, &mygo.Page{}
	bindings := cancellationBindings{windows: make(map[*mygo.Window]cancellationBinding)}
	var active atomic.Bool
	active.Store(true)
	w := &Window{done: make(chan struct{}), journalScope: "scope", journalContext: ctx, recoveryDispatch: func(fn func()) { fn() }, releaseJournalSession: func() {
		if active.Load() {
			t.Error("keeper released before admitted cancellation completed")
		}
		close(released)
	}}
	w.journal = &cancellationJournal{scope: "scope", list: func() ([]mygo.Cookie, error) { return nil, nil }}
	w.cancelHandler = func(delivery context.Context, request, operation string) error {
		if request != "claimed-request" || operation != "claimed-owner" {
			t.Error("source destruction retargeted cancellation", request, operation)
		}
		select {
		case <-sourceClosed:
		case <-delivery.Done():
			return delivery.Err()
		}
		close(cleanupStarted)
		<-allowAck
		if delivery.Err() != nil {
			t.Error("renderer destruction canceled root-owned delivery", delivery.Err())
		}
		active.Store(false)
		return nil
	}
	bindings.register(frame, page, w)
	event := &mygo.NavigateEvent{URL: cancellationNavigationURL("scope", "claimed-request", "claimed-owner")}
	if !bindings.cancellationNavigation(w, frame, page, navigationTestOrigin, navigationTestOrigin+"/", false, event) {
		t.Fatal("navigation was not committed")
	}
	// No cookie, LS, IPC or HTTP delivery is available. Only host memory knows it.
	load := newPageLoad()
	w.load = load
	w.failLoad(load, false, errWebViewReadiness, func() { close(sourceClosed) })
	select {
	case <-cleanupStarted:
	case <-time.After(time.Second):
		t.Fatal("admitted cancellation did not survive source closure")
	}
	select {
	case <-released:
		t.Fatal("last session released while memory cancellation awaited acknowledgment")
	default:
	}
	releaseCleanup()
	select {
	case <-released:
	case <-time.After(5 * time.Second):
		t.Fatal("acknowledged inbox did not release keeper")
	}
	if active.Load() || ctx.Err() != nil {
		t.Fatal("owner stayed active or renderer failure ended root recovery lifetime")
	}
}

func TestNavigationInboxDispatchDoesNotWaitForHungJournalRead(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	readStarted, releaseRead, delivered := make(chan struct{}), make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(releaseRead) })
	defer release()
	var once sync.Once
	w := &Window{done: make(chan struct{}), journalScope: "scope", journalContext: ctx, cancelHandler: func(context.Context, string, string) error { close(delivered); return nil }}
	w.journal = &cancellationJournal{scope: "scope", list: func() ([]mygo.Cookie, error) { once.Do(func() { close(readStarted) }); <-releaseRead; return nil, nil }}
	job := w.beginCancellationRecovery()
	<-readStarted
	if !w.nativeCancellationInbox().enqueue(journalCancellation{Scope: "scope", OperationID: "owner"}) {
		t.Fatal("inbox rejected target")
	}
	select {
	case <-delivered:
	case <-time.After(time.Second):
		t.Fatal("host-memory dispatch waited behind cookie SDK read")
	}
	release()
	waitJournal(t, job)
}

func TestNavigationInboxRetriesNilAndFailedHandlersAndCoalescesDuplicates(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	var mu sync.Mutex
	var handler nativeCancelHandler
	nilSeen := make(chan struct{})
	var once sync.Once
	inbox := newNativeCancellationInbox(ctx, func() nativeCancelHandler {
		mu.Lock()
		defer mu.Unlock()
		if handler == nil {
			once.Do(func() { close(nilSeen) })
		}
		return handler
	})
	ack := make(chan journalCancellation, 32)
	inbox.ack = func(record journalCancellation) { ack <- record }
	target := journalCancellation{Scope: "scope", Request: "request", OperationID: "owner"}
	inbox.enqueue(target)
	job := inbox.pendingRecovery()
	<-nilSeen
	select {
	case <-job.done:
		t.Fatal("nil guarded handler acknowledged target")
	default:
	}
	failed := make(chan struct{})
	var failOnce sync.Once
	var attempts atomic.Int32
	mu.Lock()
	handler = func(context.Context, string, string) error {
		attempts.Add(1)
		failOnce.Do(func() { close(failed) })
		return errors.New("cleanup pending")
	}
	mu.Unlock()
	inbox.retry()
	<-failed
	select {
	case <-ack:
		t.Fatal("failed handler emitted acknowledgment")
	default:
	}
	var deliveries atomic.Int32
	mu.Lock()
	handler = func(_ context.Context, request, operation string) error {
		deliveries.Add(1)
		if request != target.Request || operation != target.OperationID {
			t.Error("retry lost target")
		}
		return nil
	}
	mu.Unlock()
	for range 8 {
		inbox.enqueue(target)
	}
	waitJournal(t, job)
	if deliveries.Load() != 1 || attempts.Load() < 1 {
		t.Fatal("duplicate queue messages spawned duplicate deliveries", deliveries.Load(), attempts.Load())
	}
	select {
	case record := <-ack:
		if record != target {
			t.Fatal("wrong acknowledgment", record)
		}
	default:
		t.Fatal("successful handler did not emit exact acknowledgment")
	}
}

func TestNavigationOldTargetDoesNotStopNewOwner(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	current, active := "new-owner", true
	inbox := newNativeCancellationInbox(ctx, func() nativeCancelHandler {
		return func(_ context.Context, request, operation string) error {
			if request != "old-request" || operation != "old-owner" {
				t.Error("inbox guessed current owner")
			}
			if operation == current {
				active = false
			}
			return nil
		}
	})
	inbox.enqueue(journalCancellation{Scope: "scope", Request: "old-request", OperationID: "old-owner"})
	if job := inbox.pendingRecovery(); job != nil {
		waitJournal(t, job)
	}
	if !active || current != "new-owner" {
		t.Fatal("old memory target stopped newer connection")
	}
}

func TestMemoryReleaseBarrierJoinsNewerPendingTarget(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	ui := make(chan func(), 4)
	released := make(chan struct{})
	w := &Window{done: make(chan struct{}), journalScope: "scope", journalContext: ctx, recoveryDispatch: func(fn func()) { ui <- fn }, releaseJournalSession: func() { close(released) }}
	entered, allow := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(allow) })
	defer release()
	w.cancelHandler = func(context.Context, string, string) error { close(entered); <-allow; return nil }
	w.releaseJournalAfterRecovery(nil)
	var oldRelease func()
	select {
	case oldRelease = <-ui:
	case <-time.After(time.Second):
		t.Fatal("initial release was not queued")
	}
	w.nativeCancellationInbox().enqueue(journalCancellation{Scope: "scope", OperationID: "newly-queued-owner"})
	<-entered
	oldRelease()
	select {
	case <-released:
		t.Fatal("stale empty barrier released newer pending target")
	default:
	}
	release()
	select {
	case next := <-ui:
		next()
	case <-time.After(time.Second):
		t.Fatal("latest memory completion did not queue release")
	}
	select {
	case <-released:
	default:
		t.Fatal("acknowledged memory target retained keeper forever")
	}
}

func TestRendererFailureWithoutIntentNeverInvokesMemoryCancellation(t *testing.T) {
	var calls atomic.Int32
	released := make(chan struct{})
	w := &Window{done: make(chan struct{}), journalScope: "scope", recoveryDispatch: func(fn func()) { fn() }, releaseJournalSession: func() { close(released) }, cancelHandler: func(context.Context, string, string) error { calls.Add(1); return nil }}
	w.journal = &cancellationJournal{scope: "scope", list: func() ([]mygo.Cookie, error) { return nil, nil }}
	load := newPageLoad()
	w.load = load
	w.failLoad(load, false, errWebViewReadiness, func() {})
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("empty recovery did not release")
	}
	if calls.Load() != 0 || w.pendingNativeCancellation() != nil {
		t.Fatal("renderer loss invented cancellation")
	}
}
