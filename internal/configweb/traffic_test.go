package configweb

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/tray"
)

func TestStateExposesActualTrafficSamplesAndUnavailableSessions(t *testing.T) {
	server, _, _ := testServer(t)
	traffic := &tailsplit.TrafficSnapshot{SessionID: "traffic-session", SampledAt: time.Now().UTC(),
		Routes: map[string]tailsplit.TrafficCounters{
			"main": {UploadBytes: 17, DownloadBytes: 35}, "direct": {}, "office": {UploadBytes: 43, DownloadBytes: 101},
		}}
	for _, test := range []struct {
		name     string
		snapshot tray.Snapshot
		want     *tailsplit.TrafficSnapshot
	}{
		{"live traffic", tray.Snapshot{State: "running", Mode: "tun", Active: true, Traffic: traffic}, traffic},
		{"older helper", tray.Snapshot{State: "running", Mode: "proxy", Active: true}, nil},
		{"stopped", tray.Snapshot{State: "stopped", Mode: "tun", Traffic: traffic}, nil},
		{"login", tray.Snapshot{State: "authenticating", Mode: "login", Active: true, Traffic: traffic}, nil},
		{"failed", tray.Snapshot{State: "failed", Mode: "tun", Active: true, Traffic: traffic}, nil},
		{"stopping", tray.Snapshot{State: "stopping", Mode: "tun", Active: true, Traffic: traffic}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			server.options.Snapshot = func() tray.Snapshot { return test.snapshot }
			status, body := request(t, server, http.MethodGet, "/api/state", nil, nil)
			var state struct {
				Traffic *tailsplit.TrafficSnapshot `json:"traffic"`
			}
			if err := json.Unmarshal(body, &state); status != http.StatusOK || err != nil {
				t.Fatalf("traffic state: %d %s, %v", status, body, err)
			}
			if !reflect.DeepEqual(state.Traffic, test.want) {
				t.Fatalf("wrong traffic sample: got=%+v want=%+v", state.Traffic, test.want)
			}
			if state.Traffic != nil {
				if _, exists := state.Traffic.Routes["unavailable-exit"]; exists {
					t.Fatal("API invented a counter for an unavailable route")
				}
			}
		})
	}
}
