package tailsplit

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestInitialNoninteractiveDeviceStatesStartDeadline(t *testing.T) {
	for _, backend := range []string{"unseen", "", "Starting", "Stopped", "NeedsMachineAuth"} {
		t.Run(backend, func(t *testing.T) {
			required := []engineExit{{id: ""}, {id: "office"}}
			latest := map[string]Status{"": {BackendState: "Running", ExitVisible: true, ExitOnline: true}}
			if backend != "unseen" {
				latest["office"] = Status{BackendState: backend}
			}
			done, waiting, pending, err := connectionReadiness(MainExit, required, nil, latest, true, false)
			if err != nil || done || !waiting || pending != "office" {
				t.Fatalf("noninteractive startup remained unbounded: done=%v waiting=%v pending=%q err=%v", done, waiting, pending, err)
			}
			var deadline preflightDeadline
			defer deadline.stop()
			deadline.update(waiting, awaitingInteractiveLogin(required, latest, false))
			if deadline.timer == nil || deadline.timeout == nil {
				t.Fatal("initial required-device state did not arm a readiness deadline")
			}
		})
	}
}

func TestUnguardedOptionalDeviceWaitsForInitialStatus(t *testing.T) {
	config := focusedExitTestConfig(t)
	config.Rules.Final, config.Rules.Routes = "direct", nil
	config.Exits[0].Tailnet, config.Exits[0].MagicDNSSuffix = "", ""
	required, optional := []engineExit{{id: ""}}, []engineExit{{id: "office"}}
	latest := map[string]Status{"": {BackendState: "Running", ExitVisible: true, ExitOnline: true}}
	for _, backend := range []string{"unseen", "Starting", "Stopped"} {
		if backend != "unseen" {
			latest["office"] = Status{BackendState: backend}
		}
		if done, waiting, pending, err := connectionReadiness(MainExit, required, optional, latest, true, false); err != nil || done || !waiting || pending != "office" {
			t.Fatal("optional device was decided before it reported authentication", backend, done, waiting, pending, err)
		}
	}
	// A later Running status must retain the endpoint, without requiring a node online.
	latest["office"] = Status{BackendState: "Running"}
	if done, _, _, err := connectionReadiness(MainExit, required, optional, latest, true, false); err != nil || !done {
		t.Fatal("authenticated optional device was not admitted after initial status", done, err)
	}
	original, err := compile(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	runtime := original
	if err := isolateOptionalExits(t.Context(), config, &runtime, optional, latest); err != nil || !bytes.Equal(runtime.JSON, original.JSON) {
		t.Fatal("late authenticated optional status was still isolated", err)
	}
	var native map[string]any
	if err := json.Unmarshal(runtime.JSON, &native); err != nil || endpointTags(native)["exit-office"] == nil {
		t.Fatal("running configuration lost the authenticated optional endpoint", err)
	}
}

func TestOptionalStatusBudgetExpiresWithoutApprovingRequiredDevices(t *testing.T) {
	config := focusedExitTestConfig(t)
	config.Rules.Final, config.Rules.Routes = "direct", nil
	config.Exits[0].MagicDNSSuffix = "office.ts.net"
	required := []engineExit{{id: ""}}
	optional := []engineExit{{id: "office", tailnet: "office.ts.net"}}
	latest := map[string]Status{"": {BackendState: "Running", ExitVisible: true, ExitOnline: true}}
	if done, waiting, _, err := connectionReadiness(MainExit, required, optional, latest, true, false); err != nil || done || !waiting {
		t.Fatal("optional initial status did not get a bounded opportunity", done, waiting, err)
	}
	// At timeout only required readiness can approve starting the isolated plan.
	if ready, _, _, err := connectionReadiness(MainExit, required, nil, latest, true, false); err != nil || !ready {
		t.Fatal("ready selected gateway could not proceed after optional wait expired", ready, err)
	}
	runtime, err := compile(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	if err := isolateOptionalExits(t.Context(), config, &runtime, optional, latest); err != nil {
		t.Fatal(err)
	}
	if TailscaleEnabled {
		if err := Check(runtime); err != nil {
			t.Fatal(err)
		}
	}
	var native map[string]any
	if err := json.Unmarshal(runtime.JSON, &native); err != nil || endpointTags(native)["exit-office"] != nil {
		t.Fatal("unresponsive optional endpoint was not isolated after timeout", err)
	}
	assertBlockedExitDomainPolicy(t, native, "host.office.ts.net", "", "")
	latest[""] = Status{BackendState: "Starting"}
	if ready, waiting, _, err := connectionReadiness(MainExit, required, nil, latest, true, false); err != nil || ready || !waiting {
		t.Fatal("optional timeout approved an unready required gateway", ready, waiting, err)
	}
}
