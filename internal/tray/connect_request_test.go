package tray

import (
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
