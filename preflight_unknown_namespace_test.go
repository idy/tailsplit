package tailsplit

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestUnknownOptionalDNSOwnershipRefusesTraffic(t *testing.T) {
	for _, guard := range []string{"", "Office"} {
		for _, backend := range []string{"unseen", "Starting", "Stopped", "NeedsLogin", "Running"} {
			for _, expired := range []bool{false, true} {
				path := "normal"
				if expired {
					path = "deadline"
				}
				t.Run(guard+"/"+backend+"/"+path, func(t *testing.T) {
					config := focusedExitTestConfig(t)
					config.Rules.Final, config.Rules.Routes = "direct", nil
					config.Exits[0].Tailnet, config.Exits[0].MagicDNSSuffix = guard, ""
					beforeConfig, _ := json.Marshal(config)
					runtime, err := compile(t.Context(), config)
					if err != nil {
						t.Fatal(err)
					}
					before := bytes.Clone(runtime.JSON)
					required, optional := preflightExits(config, []engineExit{{id: ""}, {id: "office", tailnet: guard}}, true)
					latest := map[string]Status{
						"": {BackendState: "Running", ExitVisible: true, ExitOnline: true},
					}
					if backend != "unseen" {
						latest["office"] = Status{BackendState: backend, NetworkName: "Office"}
					}
					var callbacks []Status
					session := preflightSession{
						config: config, runtime: &runtime, required: required, optional: optional, requireExit: true,
						latest: latest, callback: func(status Status) { callbacks = append(callbacks, status) },
					}
					defer session.deadline.stop()
					done, _, err := session.check(t.Context(), expired)
					if done && err == nil {
						t.Fatal("unknown private namespace was approved with a direct fallback")
					}
					if expired || backend == "NeedsLogin" {
						if err == nil || !strings.Contains(err.Error(), `exit "office" has no known private DNS suffix`) || !strings.Contains(err.Error(), "refresh or sign in") {
							t.Fatal("unknown DNS ownership did not return actionable recovery", done, err)
						}
					} else if err != nil || session.deadline.timer == nil {
						t.Fatal("initial live DNS metadata did not get its bounded opportunity", err)
					}
					if !bytes.Equal(before, runtime.JSON) || len(callbacks) != 0 {
						t.Fatal("rejected ownership changed the runtime or reported successful isolation")
					}
					if after, _ := json.Marshal(config); !bytes.Equal(beforeConfig, after) {
						t.Fatal("checking unknown ownership rewrote the saved profile")
					}
				})
			}
		}
	}
}

func TestKnownOptionalDNSOwnershipRemainsIsolatable(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "explicit suffix"
		if legacy {
			name = "legacy suffix guard"
		}
		t.Run(name, func(t *testing.T) {
			config := focusedExitTestConfig(t)
			config.Rules.Final, config.Rules.Routes = "direct", nil
			config.Exits[0].Tailnet, config.Exits[0].MagicDNSSuffix = "Office", "office.ts.net"
			if legacy {
				config.Exits[0].Tailnet, config.Exits[0].MagicDNSSuffix = "office.ts.net", ""
			}
			runtime, err := compile(t.Context(), config)
			if err != nil {
				t.Fatal(err)
			}
			required, optional := preflightExits(config, []engineExit{{id: ""}, {id: "office", tailnet: config.Exits[0].Tailnet}}, true)
			session := preflightSession{
				config: config, runtime: &runtime, required: required, optional: optional, requireExit: true,
				latest: map[string]Status{
					"":       {BackendState: "Running", ExitVisible: true, ExitOnline: true},
					"office": {BackendState: "NeedsLogin"},
				},
			}
			defer session.deadline.stop()
			if done, _, err := session.check(t.Context(), false); err != nil || !done {
				t.Fatal("saved DNS ownership could not isolate the signed-out optional device", done, err)
			}
			var native map[string]any
			if err := json.Unmarshal(runtime.JSON, &native); err != nil {
				t.Fatal(err)
			}
			if endpointTags(native)["exit-office"] != nil {
				t.Fatal("signed-out endpoint remained in the actual runtime")
			}
			assertBlockedExitDomainPolicy(t, native, "host.office.ts.net", "", "")
		})
	}
}
