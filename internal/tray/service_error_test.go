package tray

import "testing"

func TestServiceErrorCategorySurvivesHelperEventsAndClearsAfterRecovery(t *testing.T) {
	c := testController(t, "manual-events")
	if err := c.StartLogin(); err != nil {
		t.Fatal(err)
	}
	waitState(t, c, "connecting")
	c.onEvent([]byte(`{"event":"error","code":"service-installation","message":"update the service"}`))
	if snapshot := c.Snapshot(); snapshot.ErrorCode != "service-installation" || snapshot.State != "failed" {
		t.Fatalf("service recovery category missing: %+v", snapshot)
	}
	if err := c.Reconfigure(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if snapshot := c.Snapshot(); snapshot.ErrorCode != "" || snapshot.Error != "" {
		t.Fatalf("recovery retained stale service error: %+v", snapshot)
	}
}
