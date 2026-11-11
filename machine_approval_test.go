package tailsplit

import (
	"strings"
	"testing"
)

func TestPendingDeviceApprovalHasActionableTimeoutDiagnostic(t *testing.T) {
	for _, id := range []string{"", "office"} {
		session := preflightSession{required: []engineExit{{id: id}}, latest: map[string]Status{id: {BackendState: "NeedsMachineAuth"}}}
		err := session.machineApprovalError()
		if err == nil || !strings.Contains(err.Error(), "administrator approval") || !strings.Contains(err.Error(), "Tailscale admin console") || !strings.Contains(err.Error(), "TUN was not enabled") {
			t.Fatalf("unhelpful error: %v", err)
		}
		name := id
		if name == "" {
			name = MainExit
		}
		if !strings.Contains(err.Error(), name) {
			t.Fatal("missing affected device", err)
		}
		session.latest[id] = Status{BackendState: "Running"}
		if err := session.machineApprovalError(); err != nil {
			t.Fatal("approved device still blocked", err)
		}
	}
	session := preflightSession{required: []engineExit{{id: ""}}, optional: []engineExit{{id: "office"}}, latest: map[string]Status{"": {BackendState: "Running"}, "office": {BackendState: "NeedsMachineAuth"}}}
	if err := session.machineApprovalError(); err != nil {
		t.Fatal("optional device replaced required readiness error", err)
	}
}
