package tray

import "testing"

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
