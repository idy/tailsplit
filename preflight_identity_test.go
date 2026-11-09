package tailsplit

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestOptionalEndpointIdentityIsCheckedWithoutRequiringItsNodeOnline(t *testing.T) {
	required := []engineExit{{id: "", tailnet: "home.example"}}
	identityOnly := []engineExit{{id: "office", tailnet: "office.example"}}
	latest := map[string]Status{
		"":       {BackendState: "Running", NetworkName: "home.example", ExitVisible: true, ExitOnline: true},
		"office": {BackendState: "Running", NetworkName: "wrong.example", ExitVisible: true},
	}
	if _, _, _, err := connectionReadiness(MainExit, required, identityOnly, latest, true, false); err == nil {
		t.Fatal("a retained optional endpoint could use the wrong Tailnet")
	}
	latest["office"] = Status{BackendState: "Running", NetworkName: "office.example", ExitVisible: true, ExitOnline: false}
	if done, _, _, err := connectionReadiness(MainExit, required, identityOnly, latest, true, false); err != nil || !done {
		t.Fatal("a guarded optional device incorrectly required its node to be online", done, err)
	}
	delete(latest, "office")
	if done, waiting, _, err := connectionReadiness(MainExit, required, identityOnly, latest, true, false); err != nil || done || !waiting {
		t.Fatal("unknown optional identity must be checked before traffic starts", done, waiting, err)
	}
	latest["office"] = Status{BackendState: "NeedsLogin"}
	if done, _, _, err := connectionReadiness(MainExit, required, identityOnly, latest, true, false); err != nil || !done {
		t.Fatal("signed-out optional identity blocked the selected ready gateway", done, err)
	}
	if blocked := unavailableOptionalExits(identityOnly, latest); !blocked["office"] || len(blocked) != 1 {
		t.Fatal("signed-out optional endpoint must be isolated", blocked)
	}
}

func TestSavedRoutingSuffixIsCheckedAlongsideTailnetGuard(t *testing.T) {
	for _, test := range []struct {
		name, guard, saved, network, actual string
		wantErr                             bool
	}{
		{name: "matching account and suffix", guard: "corp.example", saved: "old.ts.net", network: "corp.example", actual: "old.ts.net"},
		{name: "canonical suffix", guard: "corp.example", saved: "old.ts.net", network: "corp.example", actual: "OLD.TS.NET."},
		{name: "same account changed suffix", guard: "corp.example", saved: "old.ts.net", network: "corp.example", actual: "new.ts.net", wantErr: true},
		{name: "same account missing suffix", guard: "corp.example", saved: "old.ts.net", network: "corp.example", wantErr: true},
		{name: "unguarded routing suffix", saved: "old.ts.net", network: "corp.example", actual: "old.ts.net"},
		{name: "unguarded changed suffix", saved: "old.ts.net", network: "corp.example", actual: "new.ts.net", wantErr: true},
		{name: "legacy suffix guard still checks actual DNS", guard: "old.ts.net", saved: "old.ts.net", network: "old.ts.net", actual: "new.ts.net", wantErr: true},
		{name: "wrong account matching suffix", guard: "corp.example", saved: "old.ts.net", network: "other.example", actual: "old.ts.net", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			exit := engineExit{id: "office", tailnet: test.guard, magicDNSSuffix: test.saved}
			status := Status{BackendState: "Running", NetworkName: test.network, MagicDNSSuffix: test.actual, ExitVisible: true, ExitOnline: true}
			for _, optional := range []bool{false, true} {
				var required, retained []engineExit
				if optional {
					retained = []engineExit{exit}
				} else {
					required = []engineExit{exit}
				}
				done, _, _, err := connectionReadiness("office", required, retained, map[string]Status{"office": status}, true, false)
				if (err != nil) != test.wantErr || done == test.wantErr {
					t.Fatalf("optional=%v: unverified routing suffix accepted or matching suffix rejected: done=%v err=%v", optional, done, err)
				}
				if err != nil && test.network == test.guard && (!strings.Contains(err.Error(), "apply its current DNS metadata") || strings.Contains(err.Error(), "sign in")) {
					t.Fatalf("suffix metadata mismatch must offer recovery without reauthentication: %v", err)
				}
			}
		})
	}
}

func TestOptionalSuffixMismatchCannotBeIsolatedAtReadinessDeadline(t *testing.T) {
	config := focusedExitTestConfig(t)
	config.Rules.Final, config.Rules.Routes = "direct", nil
	config.Exits[0].Tailnet, config.Exits[0].MagicDNSSuffix = "corp.example", "old.ts.net"
	_, optional := preflightExits(config, []engineExit{{id: "office", tailnet: "corp.example"}}, true)
	runtime, err := CompileContext(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	before := bytes.Clone(runtime.JSON)
	// Simulate the deadline seeing a new status before its change notification
	// has been handled. Refusing only old.ts.net would leak new.ts.net.
	latest := map[string]Status{"office": {BackendState: "Running", NetworkName: "corp.example", MagicDNSSuffix: "new.ts.net"}}
	if err := isolateOptionalExits(t.Context(), config, &runtime, optional, latest); err == nil || !strings.Contains(err.Error(), "apply its current DNS metadata") {
		t.Fatal("timeout path accepted stale DNS ownership instead of requiring repair", err)
	}
	if !bytes.Equal(before, runtime.JSON) {
		t.Fatal("suffix mismatch replaced the original runtime before rejection")
	}
}

func TestPreflightBindsSuffixMetadataToTrafficDeviceOnly(t *testing.T) {
	for _, scope := range []string{"optional Split", "default Split", "Global", "focused"} {
		t.Run(scope, func(t *testing.T) {
			config := focusedExitTestConfig(t)
			config.Rules.Final, config.Rules.Routes = "direct", nil
			config.Exits[0].Tailnet, config.Exits[0].MagicDNSSuffix = "corp.example", "old.ts.net"
			exits := []engineExit{{id: "", tailnet: "primary.ts.net"}, {id: "office", tailnet: "corp.example"}}
			switch scope {
			case "default Split":
				config.Rules.DefaultExit = "office"
			case "Global":
				config.Rules.Mode, config.Rules.GlobalExit = RoutingGlobal, "office"
				exits = exits[1:]
			case "focused":
				var err error
				config, err = config.FocusExit("office")
				if err != nil {
					t.Fatal(err)
				}
				exits = exits[1:]
			}
			before, _ := json.Marshal(config)
			required, optional := preflightExits(config, exits, true)
			latest := map[string]Status{
				"":       {BackendState: "Running", NetworkName: "primary.ts.net", ExitVisible: true, ExitOnline: true},
				"office": {BackendState: "Running", NetworkName: "corp.example", MagicDNSSuffix: "new.ts.net", ExitVisible: true, ExitOnline: true},
			}
			if done, _, _, err := connectionReadiness(config.TrafficExitID(), required, optional, latest, true, false); done || err == nil || !strings.Contains(err.Error(), `exit "office"`) {
				t.Fatalf("traffic preflight lost the saved suffix for %s: done=%v err=%v", scope, done, err)
			}
			// Discovery and login still enforce the account guard, but must allow
			// the picker to observe and explicitly apply changed DNS metadata.
			for _, discover := range []bool{true, false} {
				required, optional = preflightExits(config, exits, false)
				if done, _, _, err := connectionReadiness(config.TrafficExitID(), required, optional, latest, false, discover); err != nil || !done {
					t.Fatalf("stale routing metadata prevented its own explicit repair: done=%v err=%v", done, err)
				}
			}
			latest["office"] = Status{BackendState: "Running", NetworkName: "other.example", MagicDNSSuffix: "new.ts.net"}
			if done, _, _, err := connectionReadiness(config.TrafficExitID(), required, optional, latest, false, true); done || err == nil {
				t.Fatal("routing metadata recovery discarded the saved account binding", done, err)
			}
			if after, _ := json.Marshal(config); !bytes.Equal(before, after) {
				t.Fatal("preflight rewrote saved configuration without an explicit Apply")
			}
		})
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
	if done, _, _, err := connectionReadiness(MainExit, required, optional, latest, true, false); err != nil || !done {
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
	done, waiting, _, err := connectionReadiness(MainExit, required, optional, latest, true, false)
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
	if done, _, _, err := connectionReadiness(MainExit, required, optional, latest, true, false); err != nil || done {
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

func TestRequiredSecondaryLoginReturnsRecoverableError(t *testing.T) {
	config := focusedExitTestConfig(t)
	config.Rules.DefaultExit, config.Rules.Final = MainExit, "direct"
	devices := []engineExit{{id: "", tailnet: "primary.ts.net"}, {id: "office", tailnet: "office.ts.net"}, {id: "spare"}}
	var required []engineExit
	for _, device := range devices {
		if requiresTrafficExit(config, device.id) {
			required = append(required, device)
		}
	}
	if len(required) != 2 || required[1].id != "office" {
		t.Fatal("a nonempty office route must require its device", required)
	}
	latest := map[string]Status{
		"": {BackendState: "Running", NetworkName: "primary.ts.net", ExitVisible: true, ExitOnline: true},
	}
	for _, link := range []string{"", "https://login.tailscale.com/a/unused-preflight-link"} {
		latest["office"] = Status{BackendState: "NeedsLogin", AuthURL: link}
		done, waiting, pending, err := connectionReadiness(config.TrafficExitID(), required, nil, latest, true, false)
		if err == nil || done || waiting || pending != "office" {
			t.Fatalf("required signed-out office left an unserviceable wait: done=%v waiting=%v pending=%q err=%v", done, waiting, pending, err)
		}
		if message := err.Error(); !strings.Contains(message, `exit "office"`) || !strings.Contains(message, "sign in") || strings.Contains(message, "unused-preflight-link") {
			t.Fatal("preflight must identify the sign-in action without exposing a discarded URL", message)
		}
	}
	// Signing in alone cannot approve an unavailable required gateway.
	latest["office"] = Status{BackendState: "Running", NetworkName: "office.ts.net"}
	if done, waiting, pending, err := connectionReadiness(config.TrafficExitID(), required, nil, latest, true, false); err != nil || done || !waiting || pending != "office" {
		t.Fatal("reauthorized required device bypassed exit readiness", done, waiting, pending, err)
	}
	latest["office"] = Status{BackendState: "Running", NetworkName: "office.ts.net", ExitVisible: true, ExitOnline: true}
	if done, _, _, err := connectionReadiness(config.TrafficExitID(), required, nil, latest, true, false); err != nil || !done {
		t.Fatal("retry did not complete after required office signed in and became ready", done, err)
	}
}

func TestExplicitMainFinalRequiresPrimaryReadiness(t *testing.T) {
	config := focusedExitTestConfig(t)
	config.Rules.DefaultExit, config.Rules.Final, config.Rules.Routes = "office", MainExit, nil
	devices := []engineExit{{id: "", tailnet: "primary.ts.net"}, {id: "office", tailnet: "office.ts.net"}}
	required, optional := preflightExits(config, devices, true)
	if len(required) != 2 || len(optional) != 0 {
		t.Fatal("explicit main final was treated as an optional primary", required, optional)
	}
	latest := map[string]Status{
		"":       {BackendState: "NeedsLogin"},
		"office": {BackendState: "Running", NetworkName: "office.ts.net", MagicDNSSuffix: "office.ts.net", ExitVisible: true, ExitOnline: true},
	}
	if done, waiting, pending, err := connectionReadiness(config.TrafficExitID(), required, optional, latest, true, false); done || waiting || pending != MainExit || err == nil || !strings.Contains(err.Error(), "sign in") {
		t.Fatal("required main login did not return an actionable recovery", done, waiting, pending, err)
	}
	for _, ready := range []bool{false, true} {
		latest[""] = Status{BackendState: "Running", NetworkName: "primary.ts.net", ExitVisible: true, ExitOnline: ready}
		done, waiting, _, err := connectionReadiness(config.TrafficExitID(), required, optional, latest, true, false)
		if err != nil || done != ready || waiting == ready {
			t.Fatal("additional default bypassed explicit main final readiness", done, waiting, err)
		}
	}
}

func TestSelectedAndFocusedLoginStillWaitForAuthorization(t *testing.T) {
	for _, selected := range []string{MainExit, "office"} {
		t.Run(selected, func(t *testing.T) {
			id := selected
			if id == MainExit {
				id = ""
			}
			required := []engineExit{{id: id}}
			latest := map[string]Status{id: {BackendState: "NeedsLogin"}}
			for _, requireExit := range []bool{true, false} {
				done, waiting, _, err := connectionReadiness(selected, required, nil, latest, requireExit, false)
				if err != nil || done || waiting || !awaitingInteractiveLogin(required, latest, false) {
					t.Fatal("selected/focused login lost interactive authorization", requireExit, done, waiting, err)
				}
			}
		})
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
