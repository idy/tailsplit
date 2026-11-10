package tailsplit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	singlog "github.com/sagernet/sing-box/log"
)

// PrepareTailnet authenticates and checks the selected exit without listeners,
// TUN interfaces, DNS hijacking or system routes. It releases the state lock
// before the network core starts. Use PrepareConnection when observed DNS
// metadata or optional signed-out endpoints need an updated runtime configuration.
func PrepareTailnet(ctx context.Context, config Config, callback func(Status), writer singlog.PlatformWriter) (err error) {
	if err := ValidateTrafficExit(config); err != nil {
		return err
	}
	return prepareTailnet(ctx, config, callback, writer, true, false, nil, false)
}

// PrepareConnection checks traffic readiness and returns a safe runtime plan.
// Unauthenticated optional devices with known private namespaces are omitted
// with those destinations blocked. Unknown ownership must be resolved first.
func PrepareConnection(ctx context.Context, config Config, compiled Compiled, callback func(Status), writer singlog.PlatformWriter) (Compiled, error) {
	if err := ValidateTrafficExit(config); err != nil {
		return Compiled{}, err
	}
	if err := prepareTailnet(ctx, config, callback, writer, true, false, &compiled, false); err != nil {
		return Compiled{}, err
	}
	return compiled, nil
}

// AuthenticateTailnet completes once this device is logged into the expected
// Tailnet. Login does not depend on exit visibility, availability or approval.
func AuthenticateTailnet(ctx context.Context, config Config, callback func(Status), writer singlog.PlatformWriter) error {
	return prepareTailnet(ctx, config, callback, writer, false, false, nil, false)
}

// AuthenticateNewTailnet authenticates only a new additional device. Its state
// namespace is exclusively reserved under the engine lease before any endpoint
// opens; a failed or canceled attempt keeps that namespace permanently claimed.
func AuthenticateNewTailnet(ctx context.Context, config Config, callback func(Status), writer singlog.PlatformWriter) error {
	if err := validateNewExitFocus(config); err != nil {
		return err
	}
	config, err := config.FocusExit(config.Focus)
	if err != nil {
		return err
	}
	return prepareTailnet(ctx, config, callback, writer, false, false, nil, true)
}

// DiscoverTailnet refreshes the picker without requiring an exit, opening an
// authorization browser, or capturing traffic. A signed-out device is reported
// as such and closed; only the explicit login action waits for authorization.
func DiscoverTailnet(ctx context.Context, config Config, callback func(Status), writer singlog.PlatformWriter) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return prepareTailnet(ctx, config, callback, writer, false, true, nil, false)
}

func prepareTailnet(ctx context.Context, config Config, callback func(Status), writer singlog.PlatformWriter, requireExit, discover bool, runtime *Compiled, fresh bool) (err error) {
	if len(config.ExitIDs()) == 0 {
		return nil
	}
	// Keep runtime devices available for their own Tailnet traffic, but require
	// readiness only for gateways explicitly selected by the traffic policy.
	compiled, err := compilePreflight(config, !requireExit)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	engine, err := openWithLogWriter(ctx, config, compiled, writer, fresh)
	if err != nil {
		return err
	}
	defer func() { cancel(); err = errors.Join(err, engine.Close()) }()
	if len(engine.exits) == 0 {
		return nil
	}
	if err := engine.Start(); err != nil {
		return err
	}
	required, optional := preflightExits(config, engine.exits, requireExit)
	session := preflightSession{
		config: config, runtime: runtime, required: required, optional: optional,
		requireExit: requireExit, discover: discover, callback: callback,
		latest: make(map[string]Status),
	}
	changed := make(chan struct{}, 1)
	watchError := make(chan error, 1)
	go func() {
		watchError <- engine.WatchStatus(ctx, func(status Status) {
			if session.report(status) {
				select {
				case changed <- struct{}{}:
				default:
				}
			}
		})
	}()
	defer session.deadline.stop()
	// Bound initial status delivery too; no callback must not mean no timeout.
	session.deadline.update(len(required)+len(optional) > 0, false)
	label := func(id string) string {
		if id == "" {
			return "the primary exit"
		}
		return fmt.Sprintf("exit %q", id)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-watchError:
			if err == nil {
				return errors.New("Tailnet status ended before confirming the exit; TUN was not enabled")
			}
			return err
		case <-session.deadline.timeout:
			done, pending, err := session.check(ctx, true)
			if err != nil || done {
				return err
			}
			if !requireExit {
				return errors.New("the device did not become ready or report its expected Tailnet identity")
			}
			if pending != "" {
				return fmt.Errorf("the selected node for %s is unavailable in its Tailnet; TUN was not enabled", label(pending))
			}
			return errors.New("configured exit is unavailable in this Tailnet; TUN was not enabled")
		case <-changed:
			if done, _, err := session.check(ctx, false); err != nil || done {
				return err
			}
		}
	}
}

// preflightSession serializes status publication with readiness and runtime
// finalization. No callback can publish a newer identity between the final check
// and adopting its protected runtime, or overwrite the final isolation status.
// The deadline is owned by the waiting goroutine, independently of report.
type preflightSession struct {
	config             Config
	runtime            *Compiled
	required, optional []engineExit
	requireExit        bool
	discover           bool
	callback           func(Status)
	deadline           preflightDeadline

	mu       sync.Mutex
	latest   map[string]Status
	finished bool
}

func (s *preflightSession) report(status Status) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return false
	}
	status.Phase = "preflight"
	if s.discover {
		status.Phase, status.AuthURL = "discovery", ""
	}
	if s.callback != nil {
		s.callback(status)
	}
	s.latest[status.Exit] = status
	return true
}

func (s *preflightSession) check(ctx context.Context, expired bool) (done bool, pending string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	optional := s.optional
	if expired {
		// Only optional endpoints may be isolated after their bounded wait.
		// Required gateways still need their latest identity, DNS and node state.
		optional = nil
	}
	// latest is a stable snapshot for this entire check and finalization; report
	// uses the same lock, including while delivering its caller-visible callback.
	done, waiting, pending, err := connectionReadiness(s.config.TrafficExitID(), s.required, optional, s.latest, s.requireExit, s.discover)
	if err != nil {
		return false, pending, err
	}
	if !done {
		// Only an actual interactive login pauses the deadline. Backend
		// Starting/Stopped transitions must not discard a running timeout.
		if !expired {
			s.deadline.update(waiting, awaitingInteractiveLogin(s.required, s.latest, s.discover))
		}
		return false, pending, nil
	}
	s.finished = true
	if s.requireExit {
		err = isolateOptionalExits(ctx, s.config, s.runtime, s.optional, s.latest, s.callback)
	}
	return true, "", err
}

// preflightExits checks routing metadata only when starting client traffic.
// Discovery and login must remain available to refresh stale suffix metadata;
// their account binding still comes from each saved device's Tailnet guard.
func preflightExits(config Config, exits []engineExit, requireExit bool) (required, optional []engineExit) {
	suffixes := make(map[string]string)
	if requireExit {
		for _, exit := range config.Exits {
			suffixes[exit.ID] = exit.magicDNSSuffix()
		}
	}
	for _, exit := range exits {
		exit.magicDNSSuffix = suffixes[exit.id]
		if !requireExit || requiresTrafficExit(config, exit.id) {
			required = append(required, exit)
		} else {
			// Even unguarded devices need an initial status before we decide
			// whether to retain or isolate their endpoint for this session.
			optional = append(optional, exit)
		}
	}
	return
}

type preflightDeadline struct {
	timer   *time.Timer
	timeout <-chan time.Time
}

func (d *preflightDeadline) stop() {
	if d.timer != nil {
		d.timer.Stop()
	}
	d.timer, d.timeout = nil, nil
}

func (d *preflightDeadline) update(waiting, interactiveLogin bool) {
	if interactiveLogin {
		d.stop()
	} else if waiting && d.timer == nil {
		d.timer = time.NewTimer(45 * time.Second)
		d.timeout = d.timer.C
	}
}

func awaitingInteractiveLogin(exits []engineExit, snapshot map[string]Status, discover bool) bool {
	if discover {
		return false
	}
	for _, exit := range exits {
		if snapshot[exit.id].BackendState == "NeedsLogin" {
			return true
		}
	}
	return false
}

func connectionReadiness(interactiveExit string, required, optional []engineExit, snapshot map[string]Status, requireExit, discover bool) (done, waiting bool, pending string, err error) {
	if requireExit && !discover {
		for _, exit := range required {
			id := exit.id
			if id == "" {
				id = MainExit
			}
			if id != interactiveExit && snapshot[exit.id].BackendState == "NeedsLogin" {
				// The desktop connection flow can authorize only its selected gateway.
				// Release this session so the saved exit's explicit sign-in action
				// can start a focused helper with a fresh, usable authorization URL.
				return false, false, id, fmt.Errorf("exit %q needs sign-in; open this saved exit in Exit Nodes and sign in, then connect again; TUN was not enabled", id)
			}
		}
	}
	done, waiting, pending, err = tailnetReadiness(required, snapshot, requireExit, discover)
	if err != nil {
		return
	}
	// A signed-out optional identity is handled by runtime isolation. The selected
	// gateway retains interactive login; other required devices sign in explicitly.
	identityReady, _, identityPending, identityErr := tailnetReadiness(optional, snapshot, false, true)
	if identityErr != nil {
		return false, false, "", identityErr
	}
	if !identityReady {
		done, waiting = false, true
		if pending == "" {
			pending = identityPending
		}
	}
	if requireExit && !discover {
		for _, exits := range [][]engineExit{required, optional} {
			for _, exit := range exits {
				status := snapshot[exit.id]
				if status.BackendState == "Running" && !exitRoutingDNSReady(exit, status) {
					done, waiting = false, true
					if pending == "" {
						pending = exit.id
					}
				}
			}
		}
	}
	return
}

// Running alone does not establish an extra's private DNS namespace: the SDK
// fills the suffix separately from backend state, using the netmap's self name.
// Wait for that routing metadata before retaining the endpoint, even without a
// saved suffix or account guard. Primary DNS ownership has its own configuration;
// login and discovery do not route traffic and do not need this additional check.
func exitRoutingDNSReady(exit engineExit, status Status) bool {
	return exit.id == "" || exit.id == MainExit || status.MagicDNSSuffix != ""
}

func isolateOptionalExits(ctx context.Context, config Config, runtime *Compiled, optional []engineExit, snapshot map[string]Status, callback func(Status)) error {
	// At the deadline, optional readiness no longer gates the connection. Still
	// reject identity and suffix mismatches: blocking a stale saved suffix would
	// leave the device's newly reported private suffix unprotected.
	for _, exit := range optional {
		if _, err := exitAuthenticationReady(exit, snapshot[exit.id]); err != nil {
			return err
		}
	}
	blocked := unavailableOptionalExits(optional, snapshot)
	// Minimal CLI profiles may omit DNS metadata. Protect the authenticated
	// device's observed suffix in this runtime only, without changing saved
	// mappings or the caller's shared exit slice.
	observed := false
	config.Exits = slices.Clone(config.Exits)
	for index := range config.Exits {
		exit := &config.Exits[index]
		if exit.ExitNode == "" || exit.magicDNSSuffix() != "" ||
			config.Focus != "" && config.Focus != exit.ID ||
			config.Rules.Mode == RoutingGlobal && config.TrafficExitID() != exit.ID {
			continue
		}
		// Isolation can refuse only known private names. Omitting an endpoint
		// whose namespace was never learned would leave those names on the
		// ordinary fallback, including direct DNS and routing.
		if blocked[exit.ID] {
			return fmt.Errorf("exit %q has no known private DNS suffix; refresh or sign in to this saved exit in Exit Nodes, then connect again; TUN was not enabled", exit.ID)
		}
		status := snapshot[exit.ID]
		ready, err := authenticationReady(exit.Tailnet, status)
		if err != nil {
			return err
		}
		if ready && status.MagicDNSSuffix != "" {
			exit.MagicDNSSuffix = status.MagicDNSSuffix
			observed = true
		}
	}
	if len(blocked) == 0 && !observed {
		return nil
	}
	if runtime == nil {
		return errors.New("Tailnet runtime policy needs observed DNS metadata or device isolation; a prepared runtime plan is required")
	}
	isolated, err := compileWithOptions(ctx, config, compileOptions{blockedExits: blocked})
	if err != nil {
		return err
	}
	*runtime = isolated
	if callback != nil {
		for _, exit := range optional {
			id := exit.id
			if id == "" {
				id = MainExit
			}
			if !blocked[id] {
				continue
			}
			status := snapshot[exit.id]
			status.Event, status.Exit, status.RoutingMode = "status", exit.id, config.Rules.Mode
			status.Phase, status.AuthURL, status.ExitNode = "isolated", "", ""
			status.ExitVisible, status.ExitOnline, status.PingAvailable = false, false, false
			status.ExitNodes = nil
			callback(status)
		}
	}
	return nil
}

// unavailableOptionalExits identifies endpoints that must stay closed for this
// runtime. Reconnecting or explicitly signing in reevaluates their saved state.
func unavailableOptionalExits(exits []engineExit, snapshot map[string]Status) map[string]bool {
	blocked := make(map[string]bool)
	for _, exit := range exits {
		status := snapshot[exit.id]
		ready, err := exitAuthenticationReady(exit, status)
		if !ready || err != nil || !exitRoutingDNSReady(exit, status) {
			id := exit.id
			if id == "" {
				id = MainExit
			}
			blocked[id] = true
		}
	}
	return blocked
}

// requiresTrafficExit distinguishes explicit gateways from optional saved devices.
// Optional Tailnet destinations remain compiled and fail closed for their own
// traffic when unavailable, without preventing unrelated routes from starting.
func requiresTrafficExit(config Config, id string) bool {
	if id == "" {
		id = MainExit
	}
	if config.Focus != "" || config.Rules.Mode == RoutingGlobal {
		return id == config.TrafficExitID()
	}
	if config.RequiresDefaultExit() && id == config.TrafficExitID() {
		return true
	}
	if id == config.Rules.Final {
		return true
	}
	for _, route := range config.Rules.Routes {
		if route.Exit == id && len(route.Domains)+len(route.IPs) > 0 {
			return true
		}
	}
	return false
}

func tailnetReadiness(exits []engineExit, snapshot map[string]Status, requireExit, discover bool) (done, waiting bool, pending string, err error) {
	done = true
	awaitingLogin := false
	for _, exit := range exits {
		status, seen := snapshot[exit.id]
		if !seen {
			done, waiting = false, true
			if pending == "" {
				pending = exit.id
			}
			continue
		}
		if discover && status.BackendState == "NeedsLogin" {
			continue
		}
		if status.BackendState != "Running" {
			// Every required device may finish interactive authorization. Do not
			// discard its auth URL by terminating an additional exit's helper.
			awaitingLogin = awaitingLogin || status.BackendState == "NeedsLogin"
			done, waiting = false, true
			if pending == "" {
				pending = exit.id
			}
			continue
		}
		ready, checkErr := exitAuthenticationReady(exit, status)
		if requireExit {
			ready = ready && status.ExitVisible && status.ExitOnline
		}
		if checkErr != nil {
			return false, false, "", checkErr
		}
		if !ready {
			done, waiting = false, true
			if pending == "" {
				pending = exit.id
			}
		}
	}
	if awaitingLogin {
		waiting, pending = false, ""
	}
	return
}

func compilePreflight(config Config, includePending bool) (Compiled, error) {
	config.includePending = includePending
	config.TUN.Enabled = false
	// Login, logout and exit discovery do not route client traffic and must
	// remain available even before a remote rule cache has been downloaded.
	config.Rules.GFWList = ""
	if includePending {
		// Device-only operations inspect identity, never dormant traffic policy.
		config.Rules.Mode = RoutingSplit
		config.Rules.Final = "direct"
		config.Rules.Routes = nil
		config.Rules.ProxyDomains, config.Rules.ProxyIPs = nil, nil
		// Discovery/login must work before the user chooses a traffic default.
		// This local compile copy has no listeners and never saves this choice.
		if ids := config.ExitIDs(); len(ids) > 0 && !config.hasExit(config.Rules.DefaultExit) {
			config.Rules.DefaultExit = ids[0]
		}
	}
	compiled, err := compile(context.Background(), config)
	if err != nil {
		return Compiled{}, err
	}
	var native object
	if err := json.Unmarshal(compiled.JSON, &native); err != nil {
		return Compiled{}, err
	}
	delete(native, "inbounds")
	// A listener-free device session must not consume saved FakeIP metadata.
	// sing-box still opens a cache when a desktop log writer is attached, even
	// with caching disabled. Give that cache a separate absolute state path so
	// services never fall back to cache.db in their read-only working directory.
	if experimental, ok := native["experimental"].(map[string]any); ok {
		if cache, ok := experimental["cache_file"].(map[string]any); ok {
			cache["enabled"] = false
			cache["store_fakeip"] = false
			cache["path"] = filepath.Join(filepath.Dir(cache["path"].(string)), "preflight-cache.db")
		}
	}
	compiled.JSON, err = json.Marshal(native)
	return compiled, err
}

// LogoutSavedTailnet uses the embedded client's supported logout operation.
// It has no inbound listeners or TUN and never touches the system Tailscale app.
func LogoutSavedTailnet(ctx context.Context, config Config) (err error) {
	if len(config.ExitIDs()) == 0 {
		return errors.New("no embedded Tailscale endpoint is configured")
	}
	if config.Focus == "" {
		var err error
		config, err = config.FocusExit(MainExit)
		if err != nil {
			return err
		}
	}
	compiled, err := compilePreflight(config, true)
	if err != nil {
		return err
	}
	engine, err := Open(ctx, config, compiled)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, engine.Close()) }()
	if err := engine.Start(); err != nil {
		return err
	}
	endpoint, exists := engine.box.Endpoint().Get("tailnet")
	if !exists {
		return errors.New("embedded Tailscale endpoint is missing")
	}
	tailnet, ok := endpoint.(interface{ Logout(context.Context) error })
	if !ok {
		return errors.New("embedded Tailscale endpoint does not support logout")
	}
	return tailnet.Logout(ctx)
}

func exitAuthenticationReady(exit engineExit, status Status) (bool, error) {
	ready, err := authenticationReady(exit.tailnet, status)
	if !ready || err != nil || exit.magicDNSSuffix == "" {
		return ready, err
	}
	// A suffix can change within the same authenticated account. It is routing
	// metadata, so fail before traffic starts and let explicit picker Apply update
	// the saved configuration rather than silently rewriting policy or signing out.
	canonical := func(suffix string) string {
		return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(suffix), "."))
	}
	if canonical(exit.magicDNSSuffix) != canonical(status.MagicDNSSuffix) {
		id := exit.id
		if id == "" {
			id = MainExit
		}
		return false, fmt.Errorf("exit %q MagicDNS suffix %q does not match saved routing suffix %q; refresh this saved exit in Exit Nodes and apply its current DNS metadata, then connect again; TUN was not enabled", id, status.MagicDNSSuffix, exit.magicDNSSuffix)
	}
	return true, nil
}

func preflightReady(expected string, status Status) (bool, error) {
	ready, err := authenticationReady(expected, status)
	return ready && status.ExitVisible && status.ExitOnline, err
}

func authenticationReady(expected string, status Status) (bool, error) {
	if status.BackendState != "Running" {
		return false, nil
	}
	if expected != "" && (status.NetworkName != "" || status.MagicDNSSuffix != "") &&
		!strings.EqualFold(expected, status.NetworkName) && !strings.EqualFold(expected, status.MagicDNSSuffix) {
		return false, fmt.Errorf("Tailnet %q does not match configured Tailnet %q", status.NetworkName, expected)
	}
	if expected != "" && status.NetworkName == "" && status.MagicDNSSuffix == "" {
		return false, nil
	}
	return true, nil
}
