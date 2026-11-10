package tray

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNativeConnectRequestsDoNotStartAHelperOrRewriteState(t *testing.T) {
	c := testController(t, "manual-events")
	c.RequestConnectionSetup()
	first := c.Snapshot().ConnectRequest
	if first == "" || c.Snapshot().Active || c.command != nil {
		t.Fatal("native request started work before the guided UI", c.Snapshot())
	}
	c.RequestConnectionSetup()
	if c.Snapshot().ConnectRequest == first {
		t.Fatal("a second click was not distinguishable from the first")
	}
	request := c.Snapshot().ConnectRequest
	c.mu.Lock()
	c.reset(Snapshot{State: "failed", Error: "setup failed"})
	c.mu.Unlock()
	if c.Snapshot().ConnectRequest != request {
		t.Fatal("a status reset lost the pending native request")
	}
}

func TestNativeConnectRequestClaimPreservesNewerIntent(t *testing.T) {
	c := testController(t, "manual-events")
	if c.ClaimConnectRequest("") || c.ClaimConnectRequest("unknown") {
		t.Fatal("an empty controller acknowledged a request")
	}
	c.RequestConnectionSetup()
	first := c.Snapshot().ConnectRequest
	c.RequestConnectionSetup()
	latest := c.Snapshot().ConnectRequest
	for _, request := range []string{"", "unknown", first} {
		if c.ClaimConnectRequest(request) || c.Snapshot().ConnectRequest != latest {
			t.Fatal("a stale acknowledgement consumed the newer request", request)
		}
	}
	if !c.ClaimConnectRequest(latest) || c.Snapshot().ConnectRequest != "" {
		t.Fatal("matching request was not consumed")
	}
	// Canceling guided setup resets the status; a later disconnect and a second
	// WebView must still be unable to acknowledge this already-consumed intent.
	c.mu.Lock()
	c.reset(Snapshot{State: "stopped"})
	c.mu.Unlock()
	if err := c.Stop(); err != nil {
		t.Fatal(err)
	}
	if c.ClaimConnectRequest(latest) || c.ClaimConnectRequest("") || c.Snapshot().Active {
		t.Fatal("a canceled/disconnected request was replayed", c.Snapshot())
	}
	c.RequestConnectionSetup()
	if next := c.Snapshot().ConnectRequest; next == "" || next == latest || !c.ClaimConnectRequest(next) {
		t.Fatal("consuming a request blocked a later native click")
	}
}

func TestNativeConnectRequestClaimsSurviveFocusedLogoutStatusRestore(t *testing.T) {
	for _, name := range []string{"consumed", "newer request"} {
		t.Run(name, func(t *testing.T) {
			c := testController(t, "normal")
			c.RequestConnectionSetup()
			id := c.Snapshot().ConnectRequest
			// Focused logout retains an earlier status snapshot while its helper
			// runs. Hold log publication so it cannot restore that snapshot yet.
			c.log.mu.Lock()
			unlock := sync.OnceFunc(c.log.mu.Unlock)
			defer unlock()
			done := make(chan error, 1)
			go func() { done <- c.LogoutExit("office") }()
			waitState(t, c, "logging-out")
			if !c.ClaimConnectRequest(id) {
				t.Fatal("could not claim the request while logout was in progress")
			}
			want := ""
			if name == "newer request" {
				c.RequestConnectionSetup()
				want = c.Snapshot().ConnectRequest
			}
			unlock()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("focused logout did not finish")
			}
			if got := c.Snapshot().ConnectRequest; got != want || c.ClaimConnectRequest(id) {
				t.Fatal("restoring old status changed native request authority", got, want)
			}
			if want != "" && !c.ClaimConnectRequest(want) {
				t.Fatal("focused logout consumed the newer native click")
			}
		})
	}
}

func TestNativeConnectRequestHasOnlyOneConcurrentConsumer(t *testing.T) {
	c := testController(t, "manual-events")
	c.RequestConnectionSetup()
	request := c.Snapshot().ConnectRequest
	var claimed atomic.Int32
	var consumers sync.WaitGroup
	ready := make(chan struct{})
	for range 64 {
		consumers.Go(func() {
			<-ready
			if c.ClaimConnectRequest(request) {
				claimed.Add(1)
			}
		})
	}
	close(ready)
	consumers.Wait()
	if claimed.Load() != 1 || c.Snapshot().ConnectRequest != "" {
		t.Fatal("native request did not have exactly one consumer", claimed.Load())
	}
}

func TestNativeOfferedRequestCannotAutoResumeInIndependentBrowser(t *testing.T) {
	c := testController(t, "normal")
	if err := c.ForOperation("unrelated-traffic").Start(); err != nil {
		t.Fatal(err)
	}
	waitState(t, c, "running")
	c.RequestConnectionSetup()
	request := c.Snapshot().ConnectRequest
	c.OfferNativeConnectRequest()
	// Neither IPC nor HTTP cancellation ever reaches the host. A browser with
	// independent storage must still stop at the native-offer boundary.
	if claimed, interrupted := c.ClaimPanelConnectOperation(request, "unrelated-traffic", false); claimed || !interrupted {
		t.Fatal(claimed, interrupted)
	}
	if got := c.Snapshot(); !got.Active || got.OperationID != "unrelated-traffic" || got.ConnectRequest != "" {
		t.Fatal("interruption touched unrelated running traffic", got)
	}
	if err := c.ForOperation("unrelated-traffic").Check(); err != nil {
		t.Fatal("interruption poisoned caller-supplied unrelated ID", err)
	}
	if err := c.ForOperation(request).Check(); !errors.Is(err, ErrOperationCanceled) {
		t.Fatal("interrupted request remained admissible", err)
	}
	if claimed, interrupted := c.ClaimPanelConnectOperation(request, request, true); claimed || !interrupted {
		t.Fatal("native reload revived retired offer", claimed, interrupted)
	}
	c.RequestConnectionSetup()
	fresh := c.Snapshot().ConnectRequest
	c.ObserveNativeConnectRequest(request)
	if claimed, interrupted := c.ClaimPanelConnectOperation(fresh, fresh, false); !claimed || interrupted {
		t.Fatal("dead-window retry inherited an old offer", claimed, interrupted)
	}
}

func TestColdNativeFailureAndNativeClaimKeepTheirDesignatedPaths(t *testing.T) {
	c := preparationController(t)
	c.RequestConnectionSetup()
	request := c.Snapshot().ConnectRequest
	// A cold native load failure never calls Offer, so designated fallback works.
	if claimed, interrupted := c.ClaimPanelConnectOperation(request, request, false); !claimed || interrupted {
		t.Fatal(claimed, interrupted)
	}
	c.RequestConnectionSetup()
	request = c.Snapshot().ConnectRequest
	c.ObserveNativeConnectRequest(request)
	if claimed, interrupted := c.ClaimPanelConnectOperation(request, request, true); !claimed || interrupted {
		t.Fatal("live native claim was blocked", claimed, interrupted)
	}
	c.RequestConnectionSetup()
	legacy := c.Snapshot().ConnectRequest
	c.OfferNativeConnectRequest()
	if !c.ClaimConnectOperation(legacy, legacy) {
		t.Fatal("legacy controller claim changed semantics")
	}
}

func TestNativeSnapshotOfferAndOldClaimCannotReserveOrRetireNewerRequest(t *testing.T) {
	c := preparationController(t)
	c.RequestConnectionSetup()
	old := c.Snapshot().ConnectRequest
	c.RequestConnectionSetup()
	newer := c.Snapshot().ConnectRequest
	c.ObserveNativeConnectRequest(old)
	if claimed, interrupted := c.ClaimPanelConnectOperation(old, old, false); claimed || interrupted || c.Snapshot().ConnectRequest != newer {
		t.Fatal("old response affected newer pending request", claimed, interrupted, c.Snapshot())
	}
	if claimed, interrupted := c.ClaimPanelConnectOperation(newer, newer, false); !claimed || interrupted {
		t.Fatal("stale native observation offered a newer request", claimed, interrupted)
	}
}

func TestNativeAndBrowserClaimsLinearizeAtTheOfferedRequest(t *testing.T) {
	c := preparationController(t)
	for range 32 {
		c.RequestConnectionSetup()
		request := c.Snapshot().ConnectRequest
		c.OfferNativeConnectRequest()
		type result struct{ claimed, interrupted bool }
		native, browser := make(chan result, 1), make(chan result, 1)
		ready := make(chan struct{})
		go func() {
			<-ready
			claimed, interrupted := c.ClaimPanelConnectOperation(request, request, true)
			native <- result{claimed, interrupted}
		}()
		go func() {
			<-ready
			claimed, interrupted := c.ClaimPanelConnectOperation(request, request, false)
			browser <- result{claimed, interrupted}
		}()
		close(ready)
		n, b := <-native, <-browser
		if b.claimed || c.Snapshot().ConnectRequest != "" || !n.claimed && (!n.interrupted || !b.interrupted) {
			t.Fatal("claim race escaped native offer policy", n, b, c.Snapshot())
		}
		if n.claimed {
			if err := c.ForOperation(request).Check(); err != nil {
				t.Fatal("losing fallback retired an already-claimed native request", err)
			}
		}
	}
}

func TestQueuedNativeOfferChecksRendererLifeAtAtomicCommit(t *testing.T) {
	for _, observe := range []bool{false, true} {
		t.Run(map[bool]string{false: "show-offer", true: "state-observation"}[observe], func(t *testing.T) {
			c := preparationController(t)
			c.RequestConnectionSetup()
			old := c.Snapshot().ConnectRequest
			var live atomic.Bool
			live.Store(true)
			entered, evaluated, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			markEvaluated := sync.OnceFunc(func() { close(evaluated) })
			predicate := func() bool { markEvaluated(); return live.Load() }
			c.mu.Lock()
			unlock := sync.OnceFunc(c.mu.Unlock)
			defer unlock()
			go func() {
				close(entered)
				if observe {
					c.ObserveNativeConnectRequest(old, predicate)
				} else {
					c.OfferNativeConnectRequest(predicate)
				}
				close(done)
			}()
			<-entered
			select {
			case <-evaluated:
				t.Fatal("renderer predicate was checked before acquiring controller ownership")
			case <-time.After(20 * time.Millisecond):
			}
			live.Store(false)
			// Install the later request while the queued callback still waits on
			// the ownership lock, as RequestConnectionSetup itself would do.
			c.connectRequest = "fresh-after-renderer-death"
			c.requestOperations[c.connectRequest] = c.connectRequest
			unlock()
			<-done
			if claimed, interrupted := c.ClaimPanelConnectOperation("fresh-after-renderer-death", "fresh-after-renderer-death", false); !claimed || interrupted {
				t.Fatal("dead queued callback reserved newer fallback request", claimed, interrupted)
			}
		})
	}
}
