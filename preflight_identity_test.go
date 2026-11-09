package tailsplit

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestOptionalEndpointIdentityIsCheckedWithoutRequiringItsNodeOnline(t *testing.T) {
	required := []engineExit{{id: "", tailnet: "home.example"}}
	identityOnly := []engineExit{{id: "office", tailnet: "office.example"}}
	latest := map[string]Status{
		"":       {BackendState: "Running", NetworkName: "home.example", ExitVisible: true, ExitOnline: true},
		"office": {BackendState: "Running", NetworkName: "wrong.example", ExitVisible: true},
	}
	if _, _, _, err := connectionReadiness(required, identityOnly, latest, true, false); err == nil {
		t.Fatal("a retained optional endpoint could use the wrong Tailnet")
	}
	latest["office"] = Status{BackendState: "Running", NetworkName: "office.example", ExitVisible: true, ExitOnline: false}
	if done, _, _, err := connectionReadiness(required, identityOnly, latest, true, false); err != nil || !done {
		t.Fatal("a guarded optional device incorrectly required its node to be online", done, err)
	}
	delete(latest, "office")
	if done, waiting, _, err := connectionReadiness(required, identityOnly, latest, true, false); err != nil || done || !waiting {
		t.Fatal("unknown optional identity must be checked before traffic starts", done, waiting, err)
	}
	latest["office"] = Status{BackendState: "NeedsLogin"}
	if done, _, _, err := connectionReadiness(required, identityOnly, latest, true, false); err != nil || !done {
		t.Fatal("signed-out optional identity blocked the selected ready gateway", done, err)
	}
	if blocked := unavailableOptionalExits(identityOnly, latest); !blocked["office"] || len(blocked) != 1 {
		t.Fatal("signed-out optional endpoint must be isolated", blocked)
	}
}

func TestSignedOutOptionalDeviceProducesIsolatedRuntime(t *testing.T) {
	config := focusedExitTestConfig(t)
	config.Rules.Final, config.Rules.Routes = "direct", nil
	config.Exits[0].MagicDNSSuffix = "office.ts.net"
	required := []engineExit{{id: ""}}
	optional := []engineExit{{id: "office", tailnet: "office.ts.net"}}
	latest := map[string]Status{
		"":       {BackendState: "Running", ExitVisible: true, ExitOnline: true},
		"office": {BackendState: "NeedsLogin"},
	}
	if done, _, _, err := connectionReadiness(required, optional, latest, true, false); err != nil || !done {
		t.Fatal("ready selected gateway could not proceed", done, err)
	}
	original, err := compile(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	originalJSON := bytes.Clone(original.JSON)
	isolated := original
	if err := isolateOptionalExits(t.Context(), config, &isolated, optional, latest); err != nil {
		t.Fatal(err)
	}
	if TailscaleEnabled {
		if err := Check(isolated); err != nil {
			t.Fatal(err)
		}
	}
	var native map[string]any
	if err := json.Unmarshal(isolated.JSON, &native); err != nil {
		t.Fatal(err)
	}
	if tags := endpointTags(native); tags["tailnet"] == nil || tags["exit-office"] != nil {
		t.Fatal("the actual runtime must retain the selected gateway and omit the signed-out device", tags)
	}
	assertBlockedExitDomainPolicy(t, native, "host.office.ts.net", "", "")
	if !bytes.Equal(original.JSON, originalJSON) {
		t.Fatal("isolation mutated the caller's original runtime")
	}
	if err := isolateOptionalExits(t.Context(), config, nil, optional, latest); err == nil {
		t.Fatal("error-only preparation approved an unfiltered runtime")
	}
	latest[""] = Status{BackendState: "NeedsLogin"}
	if err := isolateOptionalExits(t.Context(), config, &isolated, required, latest); err == nil {
		t.Fatal("a required gateway could be isolated instead of authenticated")
	}
}

func TestOptionalLoginDoesNotPauseRequiredGatewayDeadline(t *testing.T) {
	required := []engineExit{{id: "", tailnet: "home.example"}}
	optional := []engineExit{{id: "office", tailnet: "office.example"}}
	latest := map[string]Status{
		"":       {BackendState: "Running", NetworkName: "home.example", ExitVisible: true, ExitOnline: false},
		"office": {BackendState: "NeedsLogin"},
	}
	done, waiting, _, err := connectionReadiness(required, optional, latest, true, false)
	if err != nil || done || !waiting {
		t.Fatal("required offline gateway must retain its bounded wait", done, waiting, err)
	}
	var deadline preflightDeadline
	defer deadline.stop()
	deadline.update(waiting, awaitingInteractiveLogin(required, latest, false))
	if deadline.timer == nil {
		t.Fatal("optional authentication suspended the required gateway deadline")
	}
	latest[""] = Status{BackendState: "NeedsLogin"}
	if done, _, _, err := connectionReadiness(required, optional, latest, true, false); err != nil || done {
		t.Fatal("required authentication must finish before connection", done, err)
	}
	deadline.update(false, awaitingInteractiveLogin(required, latest, false))
	if deadline.timer != nil {
		t.Fatal("required authentication must suspend availability timeout")
	}
}

func TestUnavailableOptionalExitsPreservesIdentityAndPrimaryOwnership(t *testing.T) {
	exits := []engineExit{{id: "", tailnet: "home.example"}, {id: "office", tailnet: "office.example"}}
	for _, status := range []Status{
		{},
		{BackendState: "NeedsLogin"},
		{BackendState: "Starting"},
		{BackendState: "Running"},
		{BackendState: "Running", NetworkName: "wrong.example"},
	} {
		blocked := unavailableOptionalExits(exits, map[string]Status{"": status, "office": status})
		if len(blocked) != 2 || !blocked[MainExit] || !blocked["office"] {
			t.Fatalf("unverified identities were not isolated: %+v: %v", status, blocked)
		}
	}
	latest := map[string]Status{
		"":       {BackendState: "Running", NetworkName: "home.example"},
		"office": {BackendState: "Running", MagicDNSSuffix: "office.example"},
	}
	if blocked := unavailableOptionalExits(exits, latest); len(blocked) != 0 {
		t.Fatal("authenticated optional devices do not require an online exit node", blocked)
	}
}

func TestReadinessDeadlineOnlyPausesForInteractiveLogin(t *testing.T) {
	var deadline preflightDeadline
	defer deadline.stop()
	exits := []engineExit{{id: "office"}}
	latest := map[string]Status{"office": {BackendState: "Running", ExitVisible: true, ExitOnline: false}}
	_, waiting, _, err := tailnetReadiness(exits, latest, true, false)
	if err != nil {
		t.Fatal(err)
	}
	deadline.update(waiting, awaitingInteractiveLogin(exits, latest, false))
	original, channel := deadline.timer, deadline.timeout
	if original == nil {
		t.Fatal("offline exit did not start a bounded deadline")
	}
	for _, backend := range []string{"Starting", "Stopped"} {
		latest["office"] = Status{BackendState: backend}
		_, waiting, _, err = tailnetReadiness(exits, latest, true, false)
		if err != nil {
			t.Fatal(err)
		}
		deadline.update(waiting, awaitingInteractiveLogin(exits, latest, false))
		if deadline.timer != original || deadline.timeout != channel {
			t.Fatalf("%s transition discarded or restarted the readiness deadline", backend)
		}
	}
	latest["office"] = Status{BackendState: "NeedsLogin"}
	deadline.update(false, awaitingInteractiveLogin(exits, latest, false))
	if deadline.timer != nil || deadline.timeout != nil {
		t.Fatal("interactive login did not suspend availability timeout")
	}
	if awaitingInteractiveLogin(exits, latest, true) {
		t.Fatal("discovery queued an interactive login")
	}
}
