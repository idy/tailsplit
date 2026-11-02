package tailsplit

import (
	"testing"

	"github.com/sagernet/sing-box/adapter"
)

func TestStatusDistinguishesMissingExitFromOfflineExit(t *testing.T) {
	current := &adapter.TailscaleEndpointStatus{
		BackendState: "Running",
		NetworkName:  "independent-account",
	}
	status := connectionStatus(current, "100.64.0.10")
	if status.NetworkName != "independent-account" || status.ExitVisible || status.ExitOnline {
		t.Fatalf("missing configured exit was reported as connected: %+v", status)
	}
	current.UserGroups = []*adapter.TailscaleUserGroup{{
		Peers: []*adapter.TailscalePeer{{
			HostName: "bwg", TailscaleIPs: []string{"100.64.0.10"}, ExitNodeOption: true,
		}},
	}}
	status = connectionStatus(current, "100.64.0.10")
	if !status.ExitVisible || status.ExitOnline {
		t.Fatalf("an offline configured exit was confused with the wrong Tailnet: %+v", status)
	}
	current.ExitNode = current.UserGroups[0].Peers[0]
	current.ExitNode.Online = true
	status = connectionStatus(current, "100.64.0.10")
	if status.ExitNode != "bwg" || !status.ExitVisible || !status.ExitOnline {
		t.Fatalf("actual online exit was not reported: %+v", status)
	}
	current.ExitNode.Expired = true
	if connectionStatus(current, "100.64.0.10").ExitOnline {
		t.Fatal("an expired exit was shown as usable")
	}
}

func TestExitPickerListsOnlyApprovedExitsAndTracksExpiry(t *testing.T) {
	status := connectionStatus(&adapter.TailscaleEndpointStatus{
		BackendState: "Running",
		Self:         &adapter.TailscalePeer{HostName: "tailsplit-my-mac"},
		UserGroups: []*adapter.TailscaleUserGroup{{Peers: []*adapter.TailscalePeer{
			{HostName: "ordinary-peer", TailscaleIPs: []string{"100.64.0.1"}, Online: true},
			{HostName: "z-offline", TailscaleIPs: []string{"100.64.0.2"}, ExitNodeOption: true},
			{HostName: "b-expired", TailscaleIPs: []string{"100.64.0.3"}, ExitNodeOption: true, Online: true, Expired: true},
			{HostName: "a-online", TailscaleIPs: []string{"fd7a:115c:a1e0::1", "100.64.0.4"}, ExitNodeOption: true, Online: true},
		}}},
	}, "100.64.0.4")
	if status.Hostname != "tailsplit-my-mac" || len(status.ExitNodes) != 3 {
		t.Fatalf("picker omitted self/approved exits or listed a normal peer: %+v", status)
	}
	if first := status.ExitNodes[0]; first.ID != "100.64.0.4" || !first.Online || !first.Selected {
		t.Fatalf("online IPv4 exit selection was lost: %+v", first)
	}
	if status.ExitNodes[1].Online || status.ExitNodes[2].Online {
		t.Fatal("expired or offline exit can be selected")
	}
}

func TestCachedPreviousExitCannotSatisfyTheSelectedExitPreflight(t *testing.T) {
	selected := &adapter.TailscalePeer{
		HostName: "wanted", TailscaleIPs: []string{"100.64.0.10"}, Online: true, ExitNodeOption: true,
	}
	current := &adapter.TailscaleEndpointStatus{
		BackendState: "Running",
		ExitNode: &adapter.TailscalePeer{
			HostName: "previous", TailscaleIPs: []string{"100.64.0.11"}, Online: true,
		},
		UserGroups: []*adapter.TailscaleUserGroup{{Peers: []*adapter.TailscalePeer{selected}}},
	}
	status := connectionStatus(current, "100.64.0.10")
	if status.ExitOnline || !status.ExitVisible || status.ExitNode != "wanted" {
		t.Fatal("the old cached exit was treated as the selected one:", status)
	}
	current.ExitNode = selected
	if !connectionStatus(current, "100.64.0.10").ExitOnline {
		t.Fatal("the confirmed selected exit cannot finish preflight")
	}
}
