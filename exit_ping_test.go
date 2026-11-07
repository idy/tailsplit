package tailsplit

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
)

type fakeExitPingEndpoint struct {
	status       *adapter.TailscaleEndpointStatus
	ping         func(context.Context, string, func(*adapter.TailscalePingResult)) error
	watchStopped chan struct{}
}

func (f *fakeExitPingEndpoint) SubscribeTailscaleStatus(ctx context.Context, callback func(*adapter.TailscaleEndpointStatus)) error {
	if f.watchStopped != nil {
		defer close(f.watchStopped)
	}
	if f.status != nil {
		callback(f.status)
	}
	<-ctx.Done()
	return ctx.Err()
}

func (f *fakeExitPingEndpoint) StartTailscalePing(ctx context.Context, ip string, callback func(*adapter.TailscalePingResult)) error {
	return f.ping(ctx, ip, callback)
}

func pingFixtureStatus() *adapter.TailscaleEndpointStatus {
	return &adapter.TailscaleEndpointStatus{BackendState: "Running", NetworkName: "fixture.ts.net", UserGroups: []*adapter.TailscaleUserGroup{{Peers: []*adapter.TailscalePeer{{
		StableID: "n-fixture", HostName: "fixture-exit", DNSName: "fixture-exit.fixture.ts.net.",
		TailscaleIPs: []string{"fd7a:115c:a1e0::10", "100.64.0.10"}, Online: true, ExitNodeOption: true,
	}}}}}
}

func TestExitPingUsesAuthorizedNetmapIPAndStopsAfterOneReply(t *testing.T) {
	for _, test := range []struct {
		name   string
		result adapter.TailscalePingResult
		relay  string
	}{
		{"direct", adapter.TailscalePingResult{LatencyMs: 12.345, IsDirect: true, Endpoint: "192.0.2.1:1234"}, ""},
		{"DERP", adapter.TailscalePingResult{LatencyMs: 82.4, DERPRegionCode: "sin", DERPRegionID: 3}, "sin"},
		{"peer relay", adapter.TailscalePingResult{LatencyMs: 34, PeerRelay: "private-peer-address"}, "peer"},
	} {
		t.Run(test.name, func(t *testing.T) {
			endpoint := &fakeExitPingEndpoint{status: pingFixtureStatus(), watchStopped: make(chan struct{})}
			calls := 0
			endpoint.ping = func(ctx context.Context, ip string, callback func(*adapter.TailscalePingResult)) error {
				calls++
				if ip != "100.64.0.10" {
					t.Fatalf("ping got browser ID instead of authorized IP: %q", ip)
				}
				callback(&test.result)
				if ctx.Err() != context.Canceled {
					t.Fatal("the first reply did not stop the upstream ping stream")
				}
				return ctx.Err()
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			result, err := pingExitEndpoint(ctx, endpoint, "fixture.ts.net", "n-fixture")
			if err != nil || calls != 1 || result.ID != "n-fixture" || result.LatencyMS != test.result.LatencyMs || result.Direct != test.result.IsDirect || result.Relay != test.relay {
				t.Fatalf("unexpected ping: %+v, calls=%d, err=%v", result, calls, err)
			}
			select {
			case <-endpoint.watchStopped:
			case <-ctx.Done():
				t.Fatal("status watcher survived the ping")
			}
		})
	}
}

func TestExitPingRejectsUnauthorizedOfflineAndWrongNetworkTargets(t *testing.T) {
	for _, test := range []struct {
		name, id string
		change   func(*adapter.TailscaleEndpointStatus)
		want     error
	}{
		{"arbitrary IP", "203.0.113.1", nil, ErrPingTarget},
		{"untrusted host", "elsewhere.example", nil, ErrPingTarget},
		{"empty", "", nil, ErrPingTarget},
		{"unbounded", strings.Repeat("x", 257), nil, ErrPingTarget},
		{"offline", "100.64.0.10", func(s *adapter.TailscaleEndpointStatus) { s.UserGroups[0].Peers[0].Online = false }, ErrPingOffline},
		{"expired", "100.64.0.10", func(s *adapter.TailscaleEndpointStatus) { s.UserGroups[0].Peers[0].Expired = true }, ErrPingOffline},
		{"not approved exit", "100.64.0.10", func(s *adapter.TailscaleEndpointStatus) { s.UserGroups[0].Peers[0].ExitNodeOption = false }, ErrPingTarget},
		{"signed out", "100.64.0.10", func(s *adapter.TailscaleEndpointStatus) { s.BackendState = "NeedsLogin" }, ErrPingUnavailable},
		{"wrong network", "100.64.0.10", func(s *adapter.TailscaleEndpointStatus) { s.NetworkName = "other.ts.net" }, ErrPingUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			status := pingFixtureStatus()
			if test.change != nil {
				test.change(status)
			}
			endpoint := &fakeExitPingEndpoint{status: status, ping: func(context.Context, string, func(*adapter.TailscalePingResult)) error {
				t.Fatal("invalid target reached the Tailscale ping API")
				return nil
			}}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if _, err := pingExitEndpoint(ctx, endpoint, "fixture.ts.net", test.id); !errors.Is(err, test.want) {
				t.Fatalf("error=%v, want %v", err, test.want)
			}
		})
	}
}

func TestExitPingCancellationAndFailures(t *testing.T) {
	for _, stage := range []string{"status", "ping"} {
		t.Run(stage+" timeout", func(t *testing.T) {
			endpoint := &fakeExitPingEndpoint{ping: func(ctx context.Context, _ string, _ func(*adapter.TailscalePingResult)) error {
				<-ctx.Done()
				return ctx.Err()
			}}
			if stage == "ping" {
				endpoint.status = pingFixtureStatus()
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			if _, err := pingExitEndpoint(ctx, endpoint, "", "100.64.0.10"); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("deadline was not preserved: %v", err)
			}
		})
	}
	for _, reply := range []*adapter.TailscalePingResult{
		{Error: "private upstream login URL"}, {LatencyMs: math.NaN()}, {LatencyMs: math.Inf(1)}, {LatencyMs: -1}, nil,
	} {
		endpoint := &fakeExitPingEndpoint{status: pingFixtureStatus(), ping: func(_ context.Context, _ string, callback func(*adapter.TailscalePingResult)) error {
			callback(reply)
			return errors.New("secret upstream diagnostic")
		}}
		if _, err := pingExitEndpoint(context.Background(), endpoint, "", "100.64.0.10"); err != ErrPingFailed {
			t.Fatalf("failure was lost or exposed raw diagnostics: %v", err)
		}
	}
	engine := &Engine{}
	engine.ping.Lock()
	defer engine.ping.Unlock()
	if _, err := engine.PingExitNode(context.Background(), "100.64.0.10"); err != ErrPingBusy {
		t.Fatalf("concurrent engine ping allowed: %v", err)
	}
}

func TestExitPingSavedDeviceHonorsCancellationBeforeOpeningState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := PingSavedExitNode(ctx, Config{StateDir: "/must-not-be-opened"}, "100.64.0.10"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled request opened device state: %v", err)
	}
}
