package tailsplit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"strings"
	"sync"
	"time"

	singlog "github.com/sagernet/sing-box/log"
)

// PrepareTailnet authenticates and checks the selected exit without listeners,
// TUN interfaces, DNS hijacking or system routes. It releases the state lock
// before the network core starts.
func PrepareTailnet(ctx context.Context, config Config, callback func(Status), writer singlog.PlatformWriter) (err error) {
	if err := ValidateTrafficExit(config); err != nil {
		return err
	}
	return prepareTailnet(ctx, config, callback, writer, true, false)
}

// AuthenticateTailnet completes once this device is logged into the expected
// Tailnet. Login does not depend on exit visibility, availability or approval.
func AuthenticateTailnet(ctx context.Context, config Config, callback func(Status), writer singlog.PlatformWriter) error {
	return prepareTailnet(ctx, config, callback, writer, false, false)
}

// DiscoverTailnet refreshes the picker without requiring an exit, opening an
// authorization browser, or capturing traffic. A signed-out device is reported
// as such and closed; only the explicit login action waits for authorization.
func DiscoverTailnet(ctx context.Context, config Config, callback func(Status), writer singlog.PlatformWriter) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return prepareTailnet(ctx, config, callback, writer, false, true)
}

func prepareTailnet(ctx context.Context, config Config, callback func(Status), writer singlog.PlatformWriter, requireExit, discover bool) (err error) {
	if len(config.ExitIDs()) == 0 {
		return nil
	}
	// Connection preflight checks exactly the devices the core will run.
	compiled, err := compilePreflight(config, !requireExit)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	engine, err := OpenWithLogWriter(ctx, config, compiled, writer)
	if err != nil {
		return err
	}
	defer func() { cancel(); err = errors.Join(err, engine.Close()) }()
	if err := engine.Start(); err != nil {
		return err
	}
	// Several devices report concurrently; keep only each one's latest status.
	var latestMu sync.Mutex
	latest := map[string]Status{}
	changed := make(chan struct{}, 1)
	watchError := make(chan error, 1)
	go func() {
		watchError <- engine.WatchStatus(ctx, func(status Status) {
			status.Phase = "preflight"
			if discover {
				status.Phase = "discovery"
				status.AuthURL = ""
			}
			if callback != nil {
				callback(status)
			}
			latestMu.Lock()
			latest[status.Exit] = status
			latestMu.Unlock()
			select {
			case changed <- struct{}{}:
			default:
			}
		})
	}()
	var deadline *time.Timer
	var timeout <-chan time.Time
	defer func() {
		if deadline != nil {
			deadline.Stop()
		}
	}()
	label := func(id string) string {
		if id == "" {
			return "the primary exit"
		}
		return fmt.Sprintf("exit %q", id)
	}
	pending := ""
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-watchError:
			if err == nil {
				return errors.New("Tailnet status ended before confirming the exit; TUN was not enabled")
			}
			return err
		case <-timeout:
			if !requireExit {
				return errors.New("the logged-in device did not report its expected Tailnet identity")
			}
			if pending != "" {
				return fmt.Errorf("the selected node for %s is unavailable in its Tailnet; TUN was not enabled", label(pending))
			}
			return errors.New("configured exit is unavailable in this Tailnet; TUN was not enabled")
		case <-changed:
			latestMu.Lock()
			snapshot := maps.Clone(latest)
			latestMu.Unlock()
			done, waiting := true, false
			pending = ""
			for _, exit := range engine.exits {
				status, seen := snapshot[exit.id]
				if !seen {
					done = false
					continue
				}
				if status.BackendState == "NeedsLogin" {
					if discover {
						continue
					}
					if requireExit && exit.id != "" {
						return fmt.Errorf("sign in %s before connecting", label(exit.id))
					}
				}
				if status.BackendState != "Running" {
					done = false
					continue
				}
				ready, err := authenticationReady(exit.tailnet, status)
				if requireExit {
					ready, err = preflightReady(exit.tailnet, status)
				}
				if err != nil {
					return err
				}
				if !ready {
					done, waiting = false, true
					if pending == "" {
						pending = exit.id
					}
				}
			}
			if done {
				return nil
			}
			// Interactive login can take as long as the user needs. Once logged
			// in, a missing/offline exit must fail instead of hanging forever.
			if waiting && deadline == nil {
				deadline = time.NewTimer(45 * time.Second)
				timeout = deadline.C
			}
		}
	}
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
