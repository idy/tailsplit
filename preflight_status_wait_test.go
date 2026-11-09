package tailsplit

import (
	"bytes"
	"encoding/json"
	"strings"
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
	original, err := compile(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	runtime := original
	session := preflightSession{config: config, runtime: &runtime, required: required, optional: optional, requireExit: true, latest: latest}
	session.deadline.update(true, false)
	originalDeadline := session.deadline.timer
	defer session.deadline.stop()
	// Running and even the display network name can precede usable DNS routing
	// metadata. Neither may finalize an unchanged, unprotected runtime.
	for _, network := range []string{"", "Office"} {
		session.report(Status{Exit: "office", BackendState: "Running", NetworkName: network})
		if done, pending, err := session.check(t.Context(), false); err != nil || done || pending != "office" || !bytes.Equal(runtime.JSON, original.JSON) {
			t.Fatal("Running without DNS metadata approved an unprotected runtime", network, done, pending, err)
		}
	}
	if session.deadline.timer != originalDeadline {
		t.Fatal("waiting for optional DNS metadata discarded or restarted its deadline")
	}
	// A delayed suffix is adopted in this runtime, without requiring a saved
	// suffix, an account guard, or the optional exit node to be online.
	session.report(Status{Exit: "office", BackendState: "Running", NetworkName: "Office", MagicDNSSuffix: "office.ts.net"})
	if done, _, err := session.check(t.Context(), false); err != nil || !done {
		t.Fatal("delayed optional DNS metadata was not adopted", done, err)
	}
	var native map[string]any
	if err := json.Unmarshal(runtime.JSON, &native); err != nil || endpointTags(native)["exit-office"] == nil {
		t.Fatal("running configuration lost the authenticated optional endpoint", err)
	}
	assertBlockedExitDomainPolicy(t, native, "host.office.ts.net", "dns-tailnet-office", "exit-office")
	if config.Exits[0].MagicDNSSuffix != "" || config.Exits[0].Tailnet != "" {
		t.Fatal("observed DNS metadata changed the saved profile")
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
	if err := isolateOptionalExits(t.Context(), config, &runtime, optional, latest, nil); err != nil {
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

func TestPreflightFinalizationRechecksLatestPublishedStatus(t *testing.T) {
	for _, expired := range []bool{false, true} {
		path := "normal"
		if expired {
			path = "deadline"
		}
		for _, change := range []string{"required offline", "required identity", "optional identity", "saved suffix mismatch", "observed suffix", "optional signed out"} {
			t.Run(path+"/"+change, func(t *testing.T) {
				config := focusedExitTestConfig(t)
				config.Rules.Final, config.Rules.Routes = "direct", nil
				config.Exits[0].Tailnet, config.Exits[0].MagicDNSSuffix = "Office", ""
				if change == "saved suffix mismatch" {
					config.Exits[0].MagicDNSSuffix = "old.ts.net"
				}
				runtime, err := compile(t.Context(), config)
				if err != nil {
					t.Fatal(err)
				}
				before := bytes.Clone(runtime.JSON)
				required, optional := preflightExits(config, []engineExit{{id: "", tailnet: "primary.ts.net"}, {id: "office", tailnet: "Office"}}, true)
				session := preflightSession{
					config: config, runtime: &runtime, required: required, optional: optional, requireExit: true,
					latest: map[string]Status{
						"":       {BackendState: "Running", NetworkName: "primary.ts.net", ExitVisible: true, ExitOnline: true},
						"office": {Exit: "office", BackendState: "Running", NetworkName: "Office", MagicDNSSuffix: "old.ts.net"},
					},
				}
				defer session.deadline.stop()
				// This is the stale ready candidate from the original race. Publish
				// a newer callback before finalization, with explicit channel ordering.
				if done, _, _, err := connectionReadiness(MainExit, required, optional, session.latest, true, false); err != nil || !done {
					t.Fatal("initial candidate was not ready", done, err)
				}
				newer := session.latest["office"]
				wantErr := ""
				switch change {
				case "required offline":
					newer = session.latest[""]
					newer.ExitOnline = false
				case "required identity":
					newer = session.latest[""]
					newer.NetworkName, wantErr = "wrong.example", "does not match configured Tailnet"
				case "optional identity":
					newer.NetworkName, wantErr = "wrong.example", "does not match configured Tailnet"
				case "saved suffix mismatch":
					newer.MagicDNSSuffix, wantErr = "new.ts.net", "apply its current DNS metadata"
				case "observed suffix":
					newer.MagicDNSSuffix = "new.ts.net"
				case "optional signed out":
					newer.BackendState = "NeedsLogin"
				}
				var callbacks []Status
				publishing, release := make(chan struct{}), make(chan struct{})
				session.callback = func(status Status) {
					if session.mu.TryLock() {
						session.mu.Unlock()
						t.Error("status publication or finalization did not hold the shared lock")
					}
					callbacks = append(callbacks, status)
					if status.Phase == "preflight" {
						close(publishing)
						<-release
					}
				}
				published := make(chan struct{})
				go func() {
					session.report(newer)
					close(published)
				}()
				<-publishing // The newer callback holds the shared lock; latest is still old.
				type result struct {
					done bool
					err  error
				}
				checking, checked := make(chan struct{}), make(chan result, 1)
				go func() {
					close(checking)
					done, _, err := session.check(t.Context(), expired)
					checked <- result{done, err}
				}()
				<-checking
				select {
				case got := <-checked:
					close(release)
					<-published
					t.Fatal("finalization passed an in-flight status callback", got)
				default:
				}
				close(release)
				<-published
				got := <-checked
				done, err := got.done, got.err
				if wantErr != "" || change == "required offline" {
					if wantErr != "" && (err == nil || !strings.Contains(err.Error(), wantErr)) {
						t.Fatal("newly published identity was not rechecked", done, err)
					}
					if change == "required offline" && (done || err != nil) {
						t.Fatal("stale required readiness approved the runtime", done, err)
					}
					if !bytes.Equal(runtime.JSON, before) {
						t.Fatal("rejected finalization changed the runtime")
					}
					return
				}
				if err != nil || !done {
					t.Fatal("latest status could not finalize its protected runtime", done, err)
				}
				var native map[string]any
				if err := json.Unmarshal(runtime.JSON, &native); err != nil {
					t.Fatal(err)
				}
				if change == "observed suffix" {
					assertBlockedExitDomainPolicy(t, native, "host.new.ts.net", "dns-tailnet-office", "exit-office")
					if bytes.Contains(runtime.JSON, []byte("old.ts.net")) {
						t.Fatal("finalization adopted the stale snapshot's private suffix")
					}
				} else if endpointTags(native)["exit-office"] != nil || len(callbacks) != 2 || callbacks[1].Phase != "isolated" {
					t.Fatal("latest signed-out optional status did not finalize isolation", callbacks)
				}
				count := len(callbacks)
				if session.report(Status{Exit: "office", BackendState: "Running", MagicDNSSuffix: "later.ts.net"}) || len(callbacks) != count {
					t.Fatal("a late preflight callback overwrote the finalized status")
				}
			})
		}
	}
}

func TestMissingExtraDNSMetadataHasBoundedTrafficWait(t *testing.T) {
	for _, requiredExtra := range []bool{false, true} {
		name := "optional"
		if requiredExtra {
			name = "required"
		}
		t.Run(name, func(t *testing.T) {
			config := focusedExitTestConfig(t)
			config.Rules.Final, config.Rules.Routes = "direct", nil
			config.Exits[0].Tailnet, config.Exits[0].MagicDNSSuffix = "", ""
			if requiredExtra {
				config.Rules.Final = "office"
			}
			runtime, err := compile(t.Context(), config)
			if err != nil {
				t.Fatal(err)
			}
			before := bytes.Clone(runtime.JSON)
			required, optional := preflightExits(config, []engineExit{{id: ""}, {id: "office"}}, true)
			var statuses []Status
			session := preflightSession{
				config: config, runtime: &runtime, required: required, optional: optional, requireExit: true,
				callback: func(status Status) { statuses = append(statuses, status) },
				latest: map[string]Status{
					"":       {BackendState: "Running", ExitVisible: true, ExitOnline: true},
					"office": {BackendState: "Running", NetworkName: "Office", ExitVisible: true, ExitOnline: true},
				},
			}
			defer session.deadline.stop()
			if done, pending, err := session.check(t.Context(), false); err != nil || done || pending != "office" || session.deadline.timer == nil {
				t.Fatal("extra with unknown private DNS did not get a bounded wait", done, pending, err)
			}
			select {
			case <-session.deadline.timeout:
				t.Fatal("metadata wait expired before its readiness budget")
			default:
			}
			// DNS routing readiness must not block device-only login or discovery.
			for _, discover := range []bool{false, true} {
				if done, _, _, err := connectionReadiness(MainExit, required, optional, session.latest, false, discover); err != nil || !done {
					t.Fatal("traffic-only DNS wait leaked into login or discovery", discover, done, err)
				}
			}
			done, pending, err := session.check(t.Context(), true)
			if err != nil || done == requiredExtra || requiredExtra && pending != "office" {
				t.Fatal("metadata deadline bypassed required readiness or blocked optional isolation", done, pending, err)
			}
			if requiredExtra {
				if !bytes.Equal(runtime.JSON, before) || len(statuses) != 0 {
					t.Fatal("required metadata timeout changed runtime or isolated a required exit")
				}
				// The same required readiness check can succeed once live metadata
				// arrives; it never requires adding a saved magic_dns_suffix field.
				session.report(Status{Exit: "office", BackendState: "Running", MagicDNSSuffix: "office.ts.net", ExitVisible: true, ExitOnline: true})
				if done, _, err := session.check(t.Context(), false); err != nil || !done {
					t.Fatal("required exit could not adopt delayed live DNS metadata", done, err)
				}
				var native map[string]any
				if err := json.Unmarshal(runtime.JSON, &native); err != nil {
					t.Fatal(err)
				}
				assertBlockedExitDomainPolicy(t, native, "host.office.ts.net", "dns-tailnet-office", "exit-office")
			} else {
				var native map[string]any
				if err := json.Unmarshal(runtime.JSON, &native); err != nil || endpointTags(native)["exit-office"] != nil {
					t.Fatal("metadata-free optional endpoint survived its deadline", err)
				}
				if len(statuses) != 1 || statuses[0].Phase != "isolated" || statuses[0].BackendState != "Running" || statuses[0].ExitVisible || statuses[0].ExitOnline {
					t.Fatal("optional metadata timeout lost its actual isolated status", statuses)
				}
			}
		})
	}
}
