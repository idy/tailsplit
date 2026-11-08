package tailsplit

import "testing"

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
	if done, _, _, err := connectionReadiness(required, identityOnly, latest, true, false); err != nil || done || !awaitingInteractiveLogin(identityOnly, latest, false) {
		t.Fatal("unauthenticated guarded identities must remain pending", done, err)
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
