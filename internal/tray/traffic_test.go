package tray

import (
	"encoding/json"
	"os/exec"
	"testing"
	"time"

	"github.com/idy/tailsplit"
)

func trafficEvent(t *testing.T, id string) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{"event": "traffic", "traffic": tailsplit.TrafficSnapshot{
		SessionID: id, SampledAt: time.Now().UTC(), PrivateExits: []string{"main", "office"},
		Routes: map[string]tailsplit.TrafficCounters{"main": {UploadBytes: 23, DownloadBytes: 71}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestTrafficEventSnapshotIsIndependentAndDoesNotChangeConnectionState(t *testing.T) {
	controller := &Controller{command: &exec.Cmd{}, snapshot: Snapshot{State: "running", Mode: "tun"}}
	controller.onEvent(trafficEvent(t, "session-1"))
	snapshot := controller.Snapshot()
	if snapshot.State != "running" || snapshot.Traffic == nil || snapshot.Traffic.SessionID != "session-1" ||
		snapshot.Traffic.Routes["main"].DownloadBytes != 71 {
		t.Fatal("traffic event changed connection state or lost bytes:", snapshot)
	}
	snapshot.Traffic.Routes["main"] = tailsplit.TrafficCounters{}
	snapshot.Traffic.PrivateExits[0] = "mutated"
	snapshot.Traffic.SessionID = "mutated"
	fresh := controller.Snapshot().Traffic
	if fresh.SessionID != "session-1" || fresh.Routes["main"].UploadBytes != 23 ||
		len(fresh.PrivateExits) != 2 || fresh.PrivateExits[0] != "main" || fresh.PrivateExits[1] != "office" {
		t.Fatal("snapshot aliases controller telemetry:", fresh)
	}
}

func TestTrafficPrivateExitsRemainStableAcrossLaterStatusChanges(t *testing.T) {
	controller := &Controller{command: &exec.Cmd{}, snapshot: Snapshot{State: "running", Mode: "tun"}}
	controller.onEvent(trafficEvent(t, "compiled-session"))
	for _, event := range []string{
		`{"event":"status","backend_state":"Running","exit_online":true,"magic_dns_suffix":"shared.ts.net"}`,
		`{"event":"status","exit":"office","backend_state":"Running","exit_online":true,"magic_dns_suffix":"shared.ts.net"}`,
		`{"event":"status","exit":"office","backend_state":"NeedsLogin","magic_dns_suffix":"changed.ts.net"}`,
	} {
		controller.onEvent([]byte(event))
		got := controller.Snapshot().Traffic
		if got == nil || got.SessionID != "compiled-session" || len(got.PrivateExits) != 2 ||
			got.PrivateExits[0] != "main" || got.PrivateExits[1] != "office" {
			t.Fatal("live status metadata changed compiled private ownership:", got)
		}
	}
}

func TestTrafficSnapshotClonePreservesAuthoritativeEmptyPrivateExits(t *testing.T) {
	for _, owners := range [][]string{nil, {}} {
		snapshot := Snapshot{Traffic: &tailsplit.TrafficSnapshot{PrivateExits: owners}}
		cloned := snapshot.clone()
		if (cloned.Traffic.PrivateExits == nil) != (owners == nil) {
			t.Fatal("clone confused absent legacy ownership with authoritative empty ownership")
		}
		if owners != nil {
			data, err := json.Marshal(cloned.Traffic)
			if err != nil {
				t.Fatal(err)
			}
			var encoded map[string]json.RawMessage
			if err := json.Unmarshal(data, &encoded); err != nil {
				t.Fatal(err)
			}
			if string(encoded["private_exits"]) != "[]" {
				t.Fatalf("clone lost authoritative empty ownership: %s", data)
			}
		}
	}
}

func TestTrafficIgnoresUnavailableAndLateSessions(t *testing.T) {
	for _, test := range []struct {
		mode, state       string
		command, stopping bool
	}{
		{"login", "authenticating", true, false}, {"discovery", "discovering", true, false},
		{"proxy", "stopped", false, false}, {"tun", "failed", true, false}, {"tun", "stopping", true, true},
	} {
		controller := &Controller{snapshot: Snapshot{Mode: test.mode, State: test.state}, stopping: test.stopping}
		if test.command {
			controller.command = &exec.Cmd{}
		}
		controller.onEvent(trafficEvent(t, "late"))
		if controller.snapshot.Traffic != nil {
			t.Fatal("non-traffic or finished session accepted telemetry:", test)
		}
	}
	controller := &Controller{command: &exec.Cmd{}, snapshot: Snapshot{Mode: "proxy", State: "running"}}
	controller.onEvent([]byte(`{"event":"traffic","traffic":{}}`))
	if controller.snapshot.Traffic != nil {
		t.Fatal("malformed telemetry became a zero-speed sample")
	}
}

func TestTrafficClearsWhenHelperStopsAndRestarts(t *testing.T) {
	controller := testController(t, "manual-events")
	if err := controller.Start(); err != nil {
		t.Fatal(err)
	}
	controller.onEvent(trafficEvent(t, "old-session"))
	if controller.Snapshot().Traffic == nil {
		t.Fatal("live helper rejected telemetry")
	}
	if err := controller.Stop(); err != nil {
		t.Fatal(err)
	}
	if controller.Snapshot().Traffic != nil {
		t.Fatal("stopped helper retained telemetry")
	}
	if err := controller.Start(); err != nil {
		t.Fatal(err)
	}
	if controller.Snapshot().Traffic != nil {
		t.Fatal("new helper inherited old counters")
	}
	controller.onEvent(trafficEvent(t, "new-session"))
	if controller.Snapshot().Traffic.SessionID != "new-session" {
		t.Fatal("new helper did not receive fresh session telemetry")
	}
}
