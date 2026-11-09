package tailsplit

import (
	"context"
	"errors"
	"math"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"time"

	"github.com/idy/tailsplit/internal/pingenv"

	"github.com/sagernet/sing-box/adapter"
	singlog "github.com/sagernet/sing-box/log"
)

const ExitPingTimeout = 15 * time.Second

var (
	ErrPingUnavailable = errors.New("Tailscale ping is unavailable; sign in and wait for the device to finish connecting")
	ErrPingBusy        = errors.New("another device operation is in progress; retry the ping when it finishes")
	ErrPingTarget      = errors.New("select a discovered, authorized exit node to ping")
	ErrPingOffline     = errors.New("this exit node is offline or its authorization has expired")
	ErrPingFailed      = errors.New("the exit node did not answer the Tailscale ping; check its connection and retry")
	errPingPeerMissing = errors.New("exit node has not appeared in the current network map")
)

// ExitPingResult contains no authentication material or physical endpoint IP.
// LatencyMS is the round-trip time reported by Tailscale's discovery ping API.
type ExitPingResult struct {
	ID        string  `json:"id"`
	LatencyMS float64 `json:"latency_ms"`
	Direct    bool    `json:"direct"`
	Relay     string  `json:"relay,omitempty"`
}

// ExitPingReply is the bounded response shared by the private helper and service.
// Error codes deliberately exclude upstream errors, which may contain secrets.
type ExitPingReply struct {
	Result *ExitPingResult `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

func PingErrorCode(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "ping_timeout"
	case errors.Is(err, context.Canceled):
		return "ping_canceled"
	case errors.Is(err, ErrPingBusy):
		return "ping_busy"
	case errors.Is(err, ErrPingTarget):
		return "ping_target"
	case errors.Is(err, ErrPingOffline):
		return "ping_offline"
	case errors.Is(err, ErrPingUnavailable):
		return "ping_unavailable"
	default:
		return "ping_failed"
	}
}

func PingError(code string) error {
	switch code {
	case "ping_timeout":
		return context.DeadlineExceeded
	case "ping_canceled":
		return context.Canceled
	case "ping_busy":
		return ErrPingBusy
	case "ping_target":
		return ErrPingTarget
	case "ping_offline":
		return ErrPingOffline
	case "ping_unavailable":
		return ErrPingUnavailable
	default:
		return ErrPingFailed
	}
}

// ValidateExitPingTarget checks only discovered picker IDs, never a hostname
// supplied by the browser. The engine revalidates against its current netmap.
func ValidateExitPingTarget(nodes []ExitNode, id string) error {
	if id == "" || len(id) > 256 {
		return ErrPingTarget
	}
	for _, node := range nodes {
		if node.ID == id {
			if !node.Online {
				return ErrPingOffline
			}
			return nil
		}
	}
	return ErrPingTarget
}

type exitPingEndpoint interface {
	SubscribeTailscaleStatus(context.Context, func(*adapter.TailscaleEndpointStatus)) error
	StartTailscalePing(context.Context, string, func(*adapter.TailscalePingResult)) error
}

// PingExitNode always uses this engine's embedded device. It never closes or
// reconfigures the core, changes the selected exit, or opens another identity.
// The target may name an additional exit as "exit|node".
func (e *Engine) PingExitNode(ctx context.Context, target string) (ExitPingResult, error) {
	if !e.ping.TryLock() {
		return ExitPingResult{}, ErrPingBusy
	}
	defer e.ping.Unlock()
	exitID, id := SplitPingTarget(target)
	exit, known := e.endpoint(exitID)
	if !known {
		return ExitPingResult{}, ErrPingUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, ExitPingTimeout)
	defer cancel()
	stop := context.AfterFunc(e.ctx, cancel)
	defer stop()
	endpoint, exists := e.box.Endpoint().Get(exit.tag)
	if !exists {
		return ExitPingResult{}, ErrPingUnavailable
	}
	tailnet, ok := endpoint.(exitPingEndpoint)
	if !ok {
		return ExitPingResult{}, ErrPingUnavailable
	}
	return pingExitEndpoint(ctx, tailnet, exit.tailnet, id)
}

func pingExitEndpoint(ctx context.Context, endpoint exitPingEndpoint, expectedTailnet, id string) (ExitPingResult, error) {
	if id == "" || len(id) > 256 {
		return ExitPingResult{}, ErrPingTarget
	}
	watchCtx, stopWatch := context.WithCancel(ctx)
	defer stopWatch()
	updates := make(chan *adapter.TailscaleEndpointStatus, 1)
	watchDone := make(chan error, 1)
	go func() {
		watchDone <- endpoint.SubscribeTailscaleStatus(watchCtx, func(status *adapter.TailscaleEndpointStatus) {
			select {
			case updates <- status:
			case <-watchCtx.Done():
			}
		})
	}()
	var ip string
	for ip == "" {
		select {
		case <-ctx.Done():
			return ExitPingResult{}, ctx.Err()
		case <-watchDone:
			if err := ctx.Err(); err != nil {
				return ExitPingResult{}, err
			}
			return ExitPingResult{}, ErrPingUnavailable
		case status := <-updates:
			if status == nil {
				continue
			}
			if status.BackendState == "NeedsLogin" || status.BackendState == "Stopped" {
				return ExitPingResult{}, ErrPingUnavailable
			}
			ready, err := authenticationReady(expectedTailnet, Status{BackendState: status.BackendState,
				NetworkName: status.NetworkName, MagicDNSSuffix: status.MagicDNSSuffix})
			if err != nil {
				return ExitPingResult{}, ErrPingUnavailable
			}
			if !ready {
				continue
			}
			ip, err = authorizedExitPingIP(status, id)
			if errors.Is(err, errPingPeerMissing) {
				// Running may be reported before the peer map has arrived. Wait
				// for another status rather than rejecting a discovered exit.
				continue
			}
			if err != nil {
				return ExitPingResult{}, err
			}
		}
	}
	stopWatch()
	// StartTailscalePing is a stream using localClient.Ping(PingDisco). Cancel
	// after the first reply, so one button press cannot leave a ping loop behind.
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var result *adapter.TailscalePingResult
	err := endpoint.StartTailscalePing(pingCtx, ip, func(reply *adapter.TailscalePingResult) {
		if result == nil && reply != nil {
			copy := *reply
			result = &copy
			cancel()
		}
	})
	if ctx.Err() != nil {
		return ExitPingResult{}, ctx.Err()
	}
	if result == nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(pingCtx.Err(), context.DeadlineExceeded) {
			return ExitPingResult{}, context.DeadlineExceeded
		}
		return ExitPingResult{}, ErrPingFailed
	}
	if result.Error != "" || result.LatencyMs < 0 || math.IsNaN(result.LatencyMs) || math.IsInf(result.LatencyMs, 0) {
		return ExitPingResult{}, ErrPingFailed
	}
	reply := ExitPingResult{ID: id, LatencyMS: result.LatencyMs, Direct: result.IsDirect}
	if !reply.Direct {
		switch {
		case result.PeerRelay != "":
			reply.Relay = "peer"
		case result.DERPRegionCode != "" && len(result.DERPRegionCode) <= 32:
			reply.Relay = result.DERPRegionCode
		case result.DERPRegionID != 0:
			reply.Relay = "DERP " + strconv.Itoa(int(result.DERPRegionID))
		}
	}
	return reply, nil
}

func authorizedExitPingIP(status *adapter.TailscaleEndpointStatus, id string) (string, error) {
	found := false
	for _, group := range status.UserGroups {
		if group == nil {
			continue
		}
		for _, peer := range group.Peers {
			if peer == nil || !matchesExit(peer, id) {
				continue
			}
			found = true
			if !peer.ExitNodeOption {
				continue
			}
			if !peer.Online || peer.Expired {
				return "", ErrPingOffline
			}
			var fallback string
			for _, candidate := range peer.TailscaleIPs {
				address, err := netip.ParseAddr(candidate)
				if err != nil || address.Zone() != "" {
					continue
				}
				if address.Is4() {
					return address.String(), nil
				}
				fallback = address.String()
			}
			if fallback != "" {
				return fallback, nil
			}
		}
	}
	if found {
		return "", ErrPingTarget
	}
	return "", errPingPeerMissing
}

// Scoped pings report only their bounded result/error. Core logs must neither
// corrupt the helper's JSON stdout nor expose a newly expired login's AuthURL.
type pingLogWriter struct{}

func (pingLogWriter) WriteMessage(singlog.Level, string) {}

func savedExitPingConfig(config Config, exit string) (Config, error) {
	if exit == "" {
		exit = config.Focus
		if exit == "" {
			exit = MainExit
		}
	}
	var err error
	config, err = config.FocusExit(exit)
	if err != nil || config.Tailscale == nil {
		return Config{}, ErrPingUnavailable
	}
	endpoint := *config.Tailscale
	endpoint.AuthKeyEnv, endpoint.ExitNode = "", ""
	config.Tailscale = &endpoint
	// Normalization reapplies Focus from this inventory. Sanitize its selected
	// entry too, without changing the caller's saved selection or other exits.
	config.Exits = slices.Clone(config.Exits)
	for index := range config.Exits {
		if config.Exits[index].ID == config.Focus {
			config.Exits[index].ExitNode = ""
		}
	}
	return config, nil
}

// PingSavedExitNode opens only the saved embedded device, with no listeners,
// TUN, system DNS changes, rule downloads, auth key, or selected exit. The state
// lock prevents a second identity session; callers must reuse any active engine.
func PingSavedExitNode(ctx context.Context, config Config, target string) (result ExitPingResult, err error) {
	if err := ctx.Err(); err != nil {
		return result, err
	}
	exit, id := SplitPingTarget(target)
	if id == "" || len(id) > 256 {
		return result, ErrPingTarget
	}
	if !TailscaleEnabled || len(config.ExitIDs()) == 0 {
		return result, ErrPingUnavailable
	}
	// The service can run concurrently with other operations, so never mutate
	// its process environment. Refuse an idle engine if tsnet could use an
	// inherited key or force a login; the desktop helper strips these settings.
	if pingenv.HasAuthentication(os.Environ()) {
		return result, ErrPingUnavailable
	}
	config, err = savedExitPingConfig(config, exit)
	if err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(ctx, ExitPingTimeout)
	defer cancel()
	compiled, err := compilePreflight(config, true)
	if err != nil {
		return result, ErrPingUnavailable
	}
	engine, err := OpenWithLogWriter(ctx, config, compiled, pingLogWriter{})
	if err != nil {
		return result, ErrPingBusy
	}
	defer func() { err = errors.Join(err, engine.Close()) }()
	if err := engine.Start(); err != nil {
		return result, ErrPingUnavailable
	}
	return engine.PingExitNode(ctx, config.Focus+"|"+id)
}
