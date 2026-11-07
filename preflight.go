package tailsplit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	singlog "github.com/sagernet/sing-box/log"
)

// PrepareTailnet authenticates and checks the selected exit without listeners,
// TUN interfaces, DNS hijacking or system routes. It releases the state lock
// before the network core starts.
func PrepareTailnet(ctx context.Context, config Config, callback func(Status), writer singlog.PlatformWriter) (err error) {
	if config.Tailscale != nil && strings.TrimSpace(config.Tailscale.ExitNode) == "" {
		return errors.New("select an exit node before starting the proxy")
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
	if config.Tailscale == nil {
		return nil
	}
	compiled, err := compilePreflight(config)
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
	updates := make(chan Status, 1)
	watchError := make(chan error, 1)
	go func() {
		watchError <- engine.WatchStatus(ctx, func(status Status) {
			select {
			case updates <- status:
			default:
				select {
				case <-updates:
				default:
				}
				select {
				case updates <- status:
				case <-ctx.Done():
				}
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
			return errors.New("configured exit is unavailable in this Tailnet; TUN was not enabled")
		case status := <-updates:
			status.Phase = "preflight"
			if discover {
				status.Phase = "discovery"
				status.AuthURL = ""
			}
			if callback != nil {
				callback(status)
			}
			if discover && status.BackendState == "NeedsLogin" {
				return nil
			}
			if status.BackendState != "Running" {
				continue
			}
			ready, err := authenticationReady(config.Tailscale.Tailnet, status)
			if requireExit {
				ready, err = preflightReady(config.Tailscale.Tailnet, status)
			}
			if err != nil {
				return err
			}
			if ready {
				return nil
			}
			// Interactive login can take as long as the user needs. Once logged
			// in, a missing/offline exit must fail instead of hanging forever.
			if deadline == nil {
				deadline = time.NewTimer(45 * time.Second)
				timeout = deadline.C
			}
		}
	}
}

func compilePreflight(config Config) (Compiled, error) {
	config.TUN.Enabled = false
	// Login, logout and exit discovery do not route client traffic and must
	// remain available even before a remote rule cache has been downloaded.
	config.Rules.GFWList = ""
	compiled, err := compile(context.Background(), config)
	if err != nil {
		return Compiled{}, err
	}
	var native object
	if err := json.Unmarshal(compiled.JSON, &native); err != nil {
		return Compiled{}, err
	}
	delete(native, "inbounds")
	// A listener-free device session never serves client DNS. Opening the
	// persistent cache consumes FakeIP metadata until a clean shutdown, so an
	// interrupted preflight must use the core's in-memory store instead.
	if experimental, ok := native["experimental"].(map[string]any); ok {
		delete(experimental, "cache_file")
		if len(experimental) == 0 {
			delete(native, "experimental")
		}
	}
	compiled.JSON, err = json.Marshal(native)
	return compiled, err
}

// LogoutSavedTailnet uses the embedded client's supported logout operation.
// It has no inbound listeners or TUN and never touches the system Tailscale app.
func LogoutSavedTailnet(ctx context.Context, config Config) (err error) {
	if config.Tailscale == nil {
		return errors.New("no embedded Tailscale endpoint is configured")
	}
	compiled, err := compilePreflight(config)
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
