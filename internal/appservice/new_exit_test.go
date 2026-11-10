package appservice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/idy/tailsplit"
)

func newExitConfig(t *testing.T) tailsplit.Config {
	t.Helper()
	state := filepath.Join(t.TempDir(), "private-state")
	for _, path := range []string{state, filepath.Join(state, "exits")} {
		if err := privateDirectory(path); err != nil {
			t.Fatal(err)
		}
	}
	config, err := tailsplit.Normalize(tailsplit.Config{
		StateDir:  state,
		Tailscale: &tailsplit.TailscaleConfig{Hostname: "fixture-main"},
		Exits:     []tailsplit.ExitConfig{{ID: "saved", Hostname: "fixture-saved"}, {ID: "new-exit", Hostname: "fixture-new"}},
		TUN:       tailsplit.TUNConfig{Enabled: true},
	}, t.TempDir())
	if err == nil {
		config, err = config.FocusExit("new-exit")
	}
	if err != nil {
		t.Fatal(err)
	}
	return config
}

// The actual authenticated handler and leased broker run across net.Pipe. Only
// endpoint authentication and the engine runner are fixtures. All storage lives
// in temporary directories, with the same service lifetime lease as Serve.
func newExitPipeServer(t *testing.T, login Runner) (*Server, sessionDial) {
	t.Helper()
	base := filepath.Join(t.TempDir(), "private-service")
	if err := privateDirectory(base); err != nil {
		t.Fatal(err)
	}
	unlock, err := tailsplit.LockSavedState(base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unlock() })
	unexpected := func(context.Context, tailsplit.Config, io.Writer) error {
		t.Error("new exit authentication used a traffic or logout runner")
		return errors.New("unexpected runner")
	}
	server := &Server{UserID: "fixture-user", Base: base, Login: login, Run: unexpected, Logout: unexpected}
	directory, err := prepareRuntimeUserDirectory(base, server.userKey())
	if err != nil {
		t.Fatal(err)
	}
	if err := privateDirectory(filepath.Join(directory, "exits")); err != nil {
		t.Fatal(err)
	}
	dial := func(ctx context.Context) (net.Conn, error) {
		client, service := net.Pipe()
		done := make(chan struct{})
		go func() {
			defer close(done)
			defer service.Close()
			server.handleAuthenticated(ctx, service)
		}()
		t.Cleanup(func() {
			_ = client.Close()
			_ = service.Close()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("fixture service did not finish cleanup")
			}
		})
		return client, nil
	}
	return server, dial
}

func directNewExitRequest(t *testing.T, dial sessionDial, request Request) string {
	t.Helper()
	connection, err := dial(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(connection)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestNewExitBrokerSkipsAllMigrationAndRetainsBothClaimsOnFailure(t *testing.T) {
	for _, outcome := range []string{"success", "runner failure", "output failure", "cancel"} {
		t.Run(outcome, func(t *testing.T) {
			config := newExitConfig(t)
			// If fresh login runs the ordinary import path, these nonregular cache
			// and rule files fail collection. Unrelated identities stay untouched.
			config.Rules.GFWList = filepath.Join(config.StateDir, "ignored-rules")
			for _, path := range []string{filepath.Join(config.StateDir, "cache.db"), config.Rules.GFWList} {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			legacy := filepath.Join(config.ExitStateDir("saved"), "tailscaled.state")
			writeMigrationFile(t, legacy, "legacy-saved-identity")
			started, canceled, cleaned, release := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
			finish := sync.OnceFunc(func() { close(release) })
			defer finish()
			var server *Server
			var calls atomic.Int32
			server, dial := newExitPipeServer(t, func(ctx context.Context, received tailsplit.Config, output io.Writer) error {
				calls.Add(1)
				if received.Focus != "new-exit" || received.TUN.Enabled || len(received.Exits) != len(config.Exits) {
					t.Error("fresh authentication lost its focus/inventory or enabled TUN")
				}
				for _, root := range []string{config.StateDir, server.Base} {
					if unlock, err := tailsplit.LockSavedState(root); err == nil {
						_ = unlock()
						t.Error("new endpoint opened without its local/service lease")
					}
				}
				for _, root := range []string{config.StateDir, received.StateDir} {
					assertMigrationFile(t, filepath.Join(root, "exits", "new-exit", ".initialized"), "1\n")
				}
				assertMigrationFile(t, filepath.Join(received.ExitStateDir("saved"), "tailscaled.state"), "service-saved-identity")
				writeMigrationFile(t, filepath.Join(received.ExitStateDir("new-exit"), "tailscaled.state"), "unpublished-running-identity")
				close(started)
				defer close(cleaned)
				_ = json.NewEncoder(output).Encode(map[string]string{"event": "status", "backend_state": "Running", "exit": "new-exit"})
				if outcome == "runner failure" {
					return errors.New("fixture authentication publication failed")
				}
				if outcome == "cancel" || outcome == "output failure" {
					<-ctx.Done()
					close(canceled)
					<-release // Keep both leases through delayed engine cleanup.
					return nil
				}
				return json.NewEncoder(output).Encode(map[string]string{"event": "authenticated", "mode": "login"})
			})
			serviceDirectory := runtimeUserDirectory(server.Base, server.userKey())
			writeMigrationFile(t, filepath.Join(serviceDirectory, "tailscale", "tailscaled.state"), "service-main-identity")
			writeMigrationFile(t, filepath.Join(serviceDirectory, "exits", "saved", "tailscale", "tailscaled.state"), "service-saved-identity")
			writeMigrationFile(t, filepath.Join(serviceDirectory, ".initialized"), "existing-authority\n")
			writeMigrationFile(t, filepath.Join(serviceDirectory, "gfwlist.txt"), "existing-rules")
			writeMigrationFile(t, filepath.Join(serviceDirectory, "cache.db"), "existing-cache")
			var dials atomic.Int32
			counted := func(ctx context.Context) (net.Conn, error) { dials.Add(1); return dial(ctx) }
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			connection, err := openLeasedSession(ctx, "login-new", config, "", Info{}, counted)
			if err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			writer := io.Writer(&output)
			writeErr := errors.New("fixture broken event publication")
			if outcome == "output failure" {
				writer = failingEventWriter{writeErr}
			}
			done := make(chan error, 1)
			go func() { err := Forward(ctx, connection, writer); _ = connection.Close(); done <- err }()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("new authentication runner did not start")
			}
			if outcome == "cancel" {
				cancel()
			}
			if outcome == "cancel" || outcome == "output failure" {
				select {
				case <-canceled:
				case <-time.After(5 * time.Second):
					t.Fatal("failed publication did not cancel the fixture runner")
				}
				select {
				case err := <-done:
					t.Fatal("broker returned before engine cleanup", err)
				default:
				}
				if unlock, err := tailsplit.LockSavedState(config.StateDir); err == nil {
					_ = unlock()
					t.Fatal("broker released local lease during failed publication cleanup")
				}
				finish()
			}
			select {
			case err := <-done:
				if outcome == "output failure" && !errors.Is(err, writeErr) || outcome == "runner failure" && err == nil || (outcome == "success" || outcome == "cancel") && err != nil {
					t.Fatal("unexpected fresh authentication result", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("fresh authentication did not finish cleanup")
			}
			<-cleaned
			if calls.Load() != 1 || dials.Load() != 1 {
				t.Fatal("fresh login queried import eligibility or ran more than once", calls.Load(), dials.Load())
			}
			assertMigrationFile(t, legacy, "legacy-saved-identity")
			assertMigrationFile(t, filepath.Join(serviceDirectory, "tailscale", "tailscaled.state"), "service-main-identity")
			assertMigrationFile(t, filepath.Join(serviceDirectory, "exits", "saved", "tailscale", "tailscaled.state"), "service-saved-identity")
			assertMigrationFile(t, filepath.Join(serviceDirectory, ".initialized"), "existing-authority\n")
			assertMigrationFile(t, filepath.Join(serviceDirectory, "gfwlist.txt"), "existing-rules")
			assertMigrationFile(t, filepath.Join(serviceDirectory, "cache.db"), "existing-cache")
			for _, root := range []string{config.StateDir, serviceDirectory} {
				assertMigrationFile(t, filepath.Join(root, "exits", "new-exit", ".initialized"), "1\n")
			}
			assertMigrationFile(t, filepath.Join(serviceDirectory, "exits", "new-exit", "tailscale", "tailscaled.state"), "unpublished-running-identity")
			if _, err := openLeasedSession(context.Background(), "login-new", config, "", Info{}, counted); err == nil || dials.Load() != 1 {
				t.Fatal("retry reused an unpublished local namespace or contacted the service", err)
			}
			// Bypass the local tombstone to prove the actual service owner also
			// refuses its existing Running identity before reaching the runner.
			result := directNewExitRequest(t, dial, Request{Protocol: Protocol, Op: "login-new", Config: &config})
			if !strings.Contains(result, `"event":"error"`) || calls.Load() != 1 {
				t.Fatal("service reused unpublished state on retry", result)
			}
		})
	}
}

func TestNewExitServiceRejectsEveryExistingNamespace(t *testing.T) {
	for _, kind := range []string{"empty", "marker", "identity", "file", "symlink", "dangling-symlink"} {
		t.Run(kind, func(t *testing.T) {
			config := newExitConfig(t)
			var ran atomic.Bool
			server, dial := newExitPipeServer(t, func(context.Context, tailsplit.Config, io.Writer) error { ran.Store(true); return nil })
			directory := runtimeUserDirectory(server.Base, server.userKey())
			exits := filepath.Join(directory, "exits")
			writeMigrationFile(t, filepath.Join(directory, "tailscale", "tailscaled.state"), "primary-fixture")
			if err := os.MkdirAll(exits, 0o700); err != nil {
				t.Fatal(err)
			}
			namespace := filepath.Join(exits, "new-exit")
			existing := ""
			switch kind {
			case "file":
				existing = namespace
			case "symlink", "dangling-symlink":
				target := filepath.Join(directory, "tailscale")
				if kind == "dangling-symlink" {
					target = filepath.Join(directory, "missing")
				}
				if err := os.Symlink(target, namespace); err != nil {
					t.Skip("fixture symlink unavailable", err)
				}
			default:
				if err := os.Mkdir(namespace, 0o700); err != nil {
					t.Fatal(err)
				}
				if kind == "marker" {
					existing = filepath.Join(namespace, ".initialized")
				} else if kind == "identity" {
					existing = filepath.Join(namespace, "tailscale", "tailscaled.state")
				}
			}
			if existing != "" {
				writeMigrationFile(t, existing, "existing-fixture")
			}
			before, err := os.Lstat(namespace)
			if err != nil {
				t.Fatal(err)
			}
			result := directNewExitRequest(t, dial, Request{Protocol: Protocol, Op: "login-new", Config: &config})
			if !strings.Contains(result, `"event":"error"`) || ran.Load() {
				t.Fatal("service opened an existing namespace", result)
			}
			after, err := os.Lstat(namespace)
			if err != nil || !os.SameFile(before, after) {
				t.Fatal("service replaced the existing namespace", err)
			}
			assertMigrationFile(t, filepath.Join(directory, "tailscale", "tailscaled.state"), "primary-fixture")
			if existing != "" {
				assertMigrationFile(t, existing, "existing-fixture")
			}
		})
	}
}

func TestNewExitClaimSupportsSavedLogoutAndRejectsLaterImport(t *testing.T) {
	config := newExitConfig(t)
	server, dial := newExitPipeServer(t, func(context.Context, tailsplit.Config, io.Writer) error { return nil })
	result := directNewExitRequest(t, dial, Request{Protocol: Protocol, Op: "login-new", Config: &config})
	if !strings.Contains(result, `"event":"stopped"`) {
		t.Fatal("new device authentication failed", result)
	}
	server.active.Lock()
	defer server.active.Unlock()
	directory := runtimeUserDirectory(server.Base, server.userKey())
	received, err := serviceConfig(config, directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := claimLogoutExit(received); err != nil {
		t.Fatal("new namespace is incompatible with saved-device logout", err)
	}
	if _, err := prepareRuntimeUser(server.Base, server.userKey(), Files{ExitStates: map[string][]byte{"new-exit": []byte("stale-identity")}}); err != nil {
		t.Fatal("new namespace is incompatible with saved-state authority checks", err)
	}
	assertMigrationFile(t, filepath.Join(directory, "exits", "new-exit", ".initialized"), "1\n")
	if _, err := os.Stat(filepath.Join(received.ExitStateDir("new-exit"), "tailscaled.state")); !os.IsNotExist(err) {
		t.Fatal("later import resurrected the fresh device after logout", err)
	}
}

func TestNewExitCannotReserveBeforeLocalOrServiceLease(t *testing.T) {
	config := newExitConfig(t)
	unlock, err := tailsplit.LockSavedState(config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := openLeasedSession(t.Context(), "login-new", config, "", Info{}, func(context.Context) (net.Conn, error) {
		t.Error("broker contacted the service before taking the local lease")
		return nil, errors.New("unexpected dial")
	}); err == nil {
		t.Fatal("broker bypassed the local lease")
	}
	if _, err := os.Stat(filepath.Dir(config.ExitStateDir("new-exit"))); !os.IsNotExist(err) {
		t.Fatal("broker reserved namespace while another operation held the lease", err)
	}
	server, dial := newExitPipeServer(t, func(context.Context, tailsplit.Config, io.Writer) error {
		t.Error("busy service entered new authentication runner")
		return nil
	})
	server.active.Lock()
	result := directNewExitRequest(t, dial, Request{Protocol: Protocol, Op: "login-new", Config: &config})
	server.active.Unlock()
	if !strings.Contains(result, `"event":"error"`) {
		t.Fatal("fresh login bypassed the active service lease", result)
	}
	if _, err := os.Stat(filepath.Join(runtimeUserDirectory(server.Base, server.userKey()), "exits", "new-exit")); !os.IsNotExist(err) {
		t.Fatal("service reserved namespace while another operation held the lease", err)
	}
}

func TestNewExitRejectsInvalidFocusAndMigrationPayloads(t *testing.T) {
	config := newExitConfig(t)
	server, dial := newExitPipeServer(t, func(context.Context, tailsplit.Config, io.Writer) error {
		t.Error("invalid fresh authentication request reached a runner")
		return nil
	})
	for _, focus := range []string{"", tailsplit.MainExit, "missing", "../escape", "con"} {
		invalid := config
		invalid.Focus = focus
		if _, err := openLeasedSession(t.Context(), "login-new", invalid, "", Info{}, func(context.Context) (net.Conn, error) {
			t.Error("invalid focus reached service dial")
			return nil, errors.New("unexpected dial")
		}); err == nil {
			t.Error("broker accepted invalid new-device focus", focus)
		}
		result := directNewExitRequest(t, dial, Request{Protocol: Protocol, Op: "login-new", Config: &invalid})
		if !strings.Contains(result, `"event":"error"`) {
			t.Error("service accepted invalid new-device focus", focus, result)
		}
	}
	for _, files := range []Files{
		{TailscaleState: []byte("must-not-import")},
		{ExitStates: map[string][]byte{"new-exit": []byte("must-not-import")}},
		{ExitStates: map[string][]byte{"saved": nil}},
		{Cache: []byte("must-not-import")},
		{Rules: []byte("must-not-import")},
	} {
		result := directNewExitRequest(t, dial, Request{Protocol: Protocol, Op: "login-new", Config: &config, Files: files})
		if !strings.Contains(result, `"event":"error"`) {
			t.Fatal("service accepted migration payload for fresh authentication", result)
		}
	}
	if _, err := os.Stat(filepath.Join(runtimeUserDirectory(server.Base, server.userKey()), "exits", "new-exit")); !os.IsNotExist(err) {
		t.Fatal("rejected request claimed a fresh namespace", err)
	}
}
