package tray

import "testing"

func TestLoginFlowExplainsApprovalWithoutReopeningAuthorization(t *testing.T) {
	for _, owner := range []string{"", "office"} {
		var flow LoginFlow
		flow.Request()
		snapshot := Snapshot{State: "authenticating", Mode: "login", Focus: owner, BackendState: "NeedsMachineAuth", LoginURL: "https://login.tailscale.com/a/stale"}
		if owner != "" {
			snapshot.BackendState = "Running"
			snapshot.Exits = map[string]ExitSnapshot{owner: {BackendState: "NeedsMachineAuth"}}
		}
		if url := flow.Next(snapshot); url != "" || !flow.ShowMessage() || flow.Message() != "等待 Tailnet 管理员批准此设备" {
			t.Fatalf("approval misreported: %q %s", url, flow.Message())
		}
		snapshot.LoginURL, snapshot.State = "", "authenticated"
		snapshot.BackendState = "Running"
		if owner != "" {
			snapshot.Exits[owner] = ExitSnapshot{BackendState: "Running"}
		}
		if url := flow.Next(snapshot); url != "" || flow.ShowMessage() {
			t.Fatal("approved device did not complete login")
		}
	}
}
