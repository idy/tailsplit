package configweb

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/tray"
)

func TestActiveGlobalRejectsPendingGatewayBeforeApply(t *testing.T) {
	server, path, applied := testServer(t)
	original := "state_dir: ./state\ntailscale:\n  exit_node: 100.64.0.10\nexits:\n  - id: office\nrules:\n  mode: global\n  global_exit: main\ntun:\n  enabled: true\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	server.options.Snapshot = func() tray.Snapshot {
		return tray.Snapshot{Active: true, Mode: "tun", RoutingMode: tailsplit.RoutingGlobal, TrafficExit: tailsplit.MainExit}
	}
	_, revision, _ := tray.ReadConfigDocument(path)
	for _, changes := range []map[string]any{
		{"rules.global_exit": "office"},
		{"tailscale.exit_node": ""},
	} {
		status, body := request(t, server, "POST", "/api/config", map[string]any{
			"yaml": original, "revision": revision, "changes": changes,
		}, nil)
		current, currentRevision, err := tray.ReadConfigDocument(path)
		if status != http.StatusUnprocessableEntity || !bytes.Contains(body, []byte(`"code":"validation"`)) ||
			applied.Load() != 0 || err != nil || string(current) != original || currentRevision != revision {
			t.Fatalf("pending Global candidate crossed the pre-stop boundary: %d %s, %v", status, body, err)
		}
	}
}

func TestStoppedDraftSavesDoNotClaimRuntimeReadiness(t *testing.T) {
	for name, candidate := range map[string]string{
		"empty devices with runtime source": "tailscale: null\nrules:\n  source: ./missing-rules.txt\n  final: direct\n",
		"pending Global":                    "exits:\n  - id: office\nrules:\n  mode: global\n  global_exit: office\n  source: ./missing-rules.txt\ntun:\n  enabled: true\n",
		"unresolved Split owner":            "tailscale:\n  exit_node: 100.64.0.10\nrules:\n  final: office\n  source: ./missing-rules.txt\n",
		"retained primary private DNS":      "tailscale: null\nexits:\n  - id: office\n    exit_node: 100.101.0.11\ndns:\n  magic_domains: [removed.ts.net]\nrules:\n  default_exit: office\n  source: ./missing-rules.txt\n",
		"retained primary private route":    "tailscale: null\nexits:\n  - id: office\n    exit_node: 100.101.0.11\nrules:\n  default_exit: office\n  routes:\n    - exit: main\n      domains: [private.example]\n  source: ./missing-rules.txt\n",
	} {
		t.Run(name, func(t *testing.T) {
			server, path, applied := testServer(t)
			_, revision, _ := tray.ReadConfigDocument(path)
			candidate = "state_dir: ./state\n" + candidate
			status, body := request(t, server, "POST", "/api/validate", map[string]string{"yaml": candidate}, nil)
			if status != http.StatusUnprocessableEntity || bytes.Contains(body, []byte(`"valid":true`)) || applied.Load() != 0 {
				t.Fatalf("explicit validation claimed an incomplete draft was runnable: %d %s", status, body)
			}
			status, body = request(t, server, "POST", "/api/config", map[string]string{"yaml": candidate, "revision": revision}, nil)
			current, _, err := tray.ReadConfigDocument(path)
			if status != http.StatusOK || applied.Load() != 1 || err != nil || string(current) != candidate {
				t.Fatalf("stopped incomplete draft was not saveable: %d %s, %v", status, body, err)
			}
		})
	}
}

func TestRetainedPrimaryPrivateOwnerCannotReplaceActiveGlobal(t *testing.T) {
	server, path, applied := testServer(t)
	original := "state_dir: ./state\ntailscale: null\nexits:\n  - id: office\n    exit_node: 100.101.0.11\ndns:\n  magic_domains: [removed.ts.net]\nrules:\n  mode: global\n  default_exit: office\n  global_exit: office\ntun:\n  enabled: true\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	server.options.Snapshot = func() tray.Snapshot {
		return tray.Snapshot{Active: true, Mode: "tun", RoutingMode: tailsplit.RoutingGlobal, TrafficExit: "office"}
	}
	_, revision, _ := tray.ReadConfigDocument(path)
	status, body := request(t, server, "POST", "/api/config", map[string]any{
		"yaml": original, "revision": revision, "changes": map[string]any{"rules.mode": "split"},
	}, nil)
	current, currentRevision, err := tray.ReadConfigDocument(path)
	if status != http.StatusUnprocessableEntity || applied.Load() != 0 || err != nil || currentRevision != revision || string(current) != original {
		t.Fatalf("retained private owner stopped the active protected core: %d %s, %v", status, body, err)
	}
}

func TestConcurrentConnectRechecksDraftAtApplyGate(t *testing.T) {
	for name, candidate := range map[string]string{
		"unresolved Split": "tailscale:\n  exit_node: 100.64.0.10\nrules:\n  final: office\n",
		"pending Global":   "exits:\n  - id: office\nrules:\n  mode: global\n  global_exit: office\ntun:\n  enabled: true\n",
	} {
		t.Run(name, func(t *testing.T) {
			server, path, applied := testServer(t)
			original, revision, _ := tray.ReadConfigDocument(path)
			var active atomic.Bool
			var gate sync.Mutex
			snapshot := func() tray.Snapshot {
				return tray.Snapshot{Active: active.Load(), Mode: "tun", RoutingMode: tailsplit.RoutingGlobal, TrafficExit: tailsplit.MainExit}
			}
			server.options.Snapshot = snapshot
			queued, connected := make(chan struct{}), make(chan struct{})
			server.options.Apply = func(_ context.Context, check func(tray.Snapshot) error, update func() error) error {
				// The request passed the inactive HTTP snapshot, but has not yet
				// acquired the operation gate used by Connect and checked apply.
				close(queued)
				<-connected
				gate.Lock()
				defer gate.Unlock()
				if err := check(snapshot()); err != nil {
					return err
				}
				applied.Add(1)
				return update()
			}
			type response struct {
				status int
				body   []byte
			}
			done := make(chan response, 1)
			go func() {
				status, body := request(t, server, "POST", "/api/config", map[string]string{
					"yaml": "state_dir: ./state\n" + candidate, "revision": revision,
				}, nil)
				done <- response{status, body}
			}()
			select {
			case <-queued:
			case <-time.After(5 * time.Second):
				close(connected)
				t.Fatal("draft save did not reach the apply gate")
			}
			gate.Lock()
			active.Store(true) // Concurrent Connect wins the operation gate.
			gate.Unlock()
			close(connected)
			select {
			case result := <-done:
				current, currentRevision, err := tray.ReadConfigDocument(path)
				if result.status != http.StatusUnprocessableEntity || !bytes.Contains(result.body, []byte(`"code":"validation"`)) ||
					applied.Load() != 0 || !active.Load() || err != nil || !bytes.Equal(current, original) || currentRevision != revision {
					t.Fatalf("stale inactive check allowed connected draft apply: %d %s, %v", result.status, result.body, err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("checked apply did not finish")
			}
		})
	}
}

func TestGlobalGatewayGuardUsesRunningDeviceInsteadOfSavedSelection(t *testing.T) {
	config := tailsplit.Config{Rules: tailsplit.RulesConfig{Mode: tailsplit.RoutingGlobal, GlobalExit: "office"}}
	snapshot := tray.Snapshot{Active: true, Mode: "tun", RoutingMode: tailsplit.RoutingGlobal, TrafficExit: tailsplit.MainExit}
	// The file may already select office after an external edit, and cached
	// office status may look ready. Neither verifies it in the running Global core.
	snapshot.Exits = map[string]tray.ExitSnapshot{"office": {BackendState: "Running", ExitVisible: true, ExitOnline: true}}
	if err := checkCandidateSession(snapshot, config, nil); err == nil {
		t.Fatal("changing the actual protected traffic device used cached or saved state")
	}
	config.Rules.GlobalExit = tailsplit.MainExit
	if err := checkCandidateSession(snapshot, config, nil); err != nil {
		t.Fatal("retaining the actual Global gateway was rejected:", err)
	}
	config.Rules.Mode, config.Rules.DefaultExit = tailsplit.RoutingSplit, "office"
	if err := checkCandidateSession(snapshot, config, nil); err != nil {
		t.Fatal("explicitly switching to Rule-based mode with another default was rejected:", err)
	}
	config.Rules.Mode, config.Rules.GlobalExit = tailsplit.RoutingGlobal, "office"
	snapshot.Active = false // Explicit disconnect after consent.
	if err := checkCandidateSession(snapshot, config, nil); err != nil {
		t.Fatal("explicitly disconnected gateway change was rejected:", err)
	}
}
