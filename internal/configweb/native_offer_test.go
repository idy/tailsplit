package configweb

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/idy/tailsplit/internal/tray"
)

func nativeOfferServer(t *testing.T) (*Server, *tray.Controller) {
	t.Helper()
	server, path, _ := testServer(t)
	controller, err := tray.New(tray.Options{Helper: "unused", ConfigPath: path, LogPath: filepath.Join(t.TempDir(), "core.log")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = controller.Close() })
	server.options.Snapshot = controller.Snapshot
	server.options.ObserveNativeConnectRequest = func(id string) { controller.ObserveNativeConnectRequest(id) }
	server.options.ClaimPanelConnectOperation = controller.ClaimPanelConnectOperation
	return server, controller
}

func TestNativeStateOfferGuardsFallbackWhenBothCancelTransportsAreLost(t *testing.T) {
	server, controller := nativeOfferServer(t)
	controller.RequestConnectionSetup()
	id := controller.Snapshot().ConnectRequest
	status, _ := request(t, server, "GET", "/api/state", nil, map[string]string{"X-TailSplit-Panel": "native"})
	if status != http.StatusOK {
		t.Fatal(status)
	}
	// No /api/cancel or native callback is delivered. This request has a new
	// browser's capability, with none of the originating WebView's storage.
	status, body := request(t, server, "POST", "/api/connect-request", map[string]string{"request": id, "operation_id": id}, nil)
	var result map[string]bool
	if json.Unmarshal(body, &result) != nil || status != http.StatusOK || result["claimed"] || !result["interrupted"] {
		t.Fatal(status, string(body))
	}
	if got := controller.Snapshot(); got.Active || got.ConnectRequest != "" {
		t.Fatal("fallback resumed interrupted setup", got)
	}
	if err := controller.ForOperation(id).Check(); !errors.Is(err, tray.ErrOperationCanceled) {
		t.Fatal("interrupted request was not revoked", err)
	}
	controller.RequestConnectionSetup()
	fresh := controller.Snapshot().ConnectRequest
	status, body = request(t, server, "POST", "/api/connect-request", map[string]string{"request": fresh, "operation_id": fresh}, nil)
	if json.Unmarshal(body, &result) != nil || status != http.StatusOK || !result["claimed"] || result["interrupted"] {
		t.Fatal("fresh dead-window request could not use browser fallback", status, string(body))
	}
}

func TestNativeClaimHintAndColdFailureFallbackRemainEligible(t *testing.T) {
	server, controller := nativeOfferServer(t)
	controller.RequestConnectionSetup()
	id := controller.Snapshot().ConnectRequest
	// Browser observation is not a native offer. A cold load failure can use it.
	status, _ := request(t, server, "GET", "/api/state", nil, nil)
	if status != http.StatusOK {
		t.Fatal(status)
	}
	status, body := request(t, server, "POST", "/api/connect-request", map[string]string{"request": id}, nil)
	var result map[string]bool
	if json.Unmarshal(body, &result) != nil || status != http.StatusOK || !result["claimed"] || result["interrupted"] {
		t.Fatal(status, string(body))
	}
	controller.RequestConnectionSetup()
	id = controller.Snapshot().ConnectRequest
	controller.OfferNativeConnectRequest()
	status, body = request(t, server, "POST", "/api/connect-request", map[string]string{"request": id, "operation_id": id}, map[string]string{"X-TailSplit-Panel": "native"})
	if json.Unmarshal(body, &result) != nil || status != http.StatusOK || !result["claimed"] || result["interrupted"] {
		t.Fatal("native claim was incorrectly treated as fallback", status, string(body))
	}
}

func TestNativeStateSnapshotOffersOnlyItsExactPendingRequest(t *testing.T) {
	server, controller := nativeOfferServer(t)
	controller.RequestConnectionSetup()
	old := controller.Snapshot()
	newRequest := make(chan string, 1)
	server.options.Snapshot = func() tray.Snapshot {
		controller.RequestConnectionSetup()
		newRequest <- controller.Snapshot().ConnectRequest
		return old
	}
	status, _ := request(t, server, "GET", "/api/state", nil, map[string]string{"X-TailSplit-Panel": "native"})
	newer := <-newRequest
	if status != http.StatusOK || newer == "" {
		t.Fatal(status, newer)
	}
	if claimed, interrupted := controller.ClaimPanelConnectOperation(newer, newer, false); !claimed || interrupted {
		t.Fatal("old snapshot native header offered newer unseen request", claimed, interrupted)
	}
}
