package appservice

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/idy/tailsplit"
)

func TestExitPingServiceReusesActiveEngineWithoutStoppingIt(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	path, server, cancel, done := testServer(t, os.Getuid(), func(ctx context.Context, config tailsplit.Config, _ io.Writer) error {
		unregister := RegisterExitPing(ctx, func(ctx context.Context, id string) (tailsplit.ExitPingResult, error) {
			if ctx.Err() != nil || id != "100.64.0.10" || !config.TUN.Enabled {
				t.Error("live ping altered the active configuration or context")
			}
			return tailsplit.ExitPingResult{ID: id, LatencyMS: 12.3, Direct: true}, nil
		})
		defer unregister()
		close(started)
		<-ctx.Done()
		close(stopped)
		return nil
	})
	server.Ping = func(context.Context, tailsplit.Config, string) (tailsplit.ExitPingResult, error) {
		t.Error("active ping opened a second device")
		return tailsplit.ExitPingResult{}, nil
	}
	connection, _ := session(t, path, Request{Protocol: Protocol, Op: "start", Config: &tailsplit.Config{TUN: tailsplit.TUNConfig{Enabled: true}}})
	<-started
	ping, response := session(t, path, Request{Protocol: Protocol, Op: "ping", Target: "100.64.0.10"})
	var reply tailsplit.ExitPingReply
	if err := response.Decode(&reply); err != nil || reply.Result == nil || reply.Error != "" || reply.Result.LatencyMS != 12.3 {
		t.Fatalf("active ping failed: %+v %v", reply, err)
	}
	_ = ping.Close()
	select {
	case <-stopped:
		t.Fatal("ping interrupted the active network session")
	default:
	}
	_ = connection.Close()
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestExitPingServiceIdleSessionIsSerializedBoundedAndCanceled(t *testing.T) {
	started := make(chan tailsplit.Config, 1)
	stopped := make(chan struct{})
	path, server, cancel, done := testServer(t, os.Getuid(), func(context.Context, tailsplit.Config, io.Writer) error {
		t.Error("network startup competed with an idle ping")
		return nil
	})
	server.Ping = func(ctx context.Context, config tailsplit.Config, id string) (tailsplit.ExitPingResult, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > tailsplit.ExitPingTimeout || id != "100.64.0.10" {
			t.Error("idle ping has no bounded deadline or lost its target")
		}
		started <- config
		<-ctx.Done()
		close(stopped)
		return tailsplit.ExitPingResult{}, ctx.Err()
	}
	ping, _ := session(t, path, Request{Protocol: Protocol, Op: "ping", Target: "100.64.0.10", Config: &tailsplit.Config{
		StateDir: "/untrusted", Tailscale: &tailsplit.TailscaleConfig{}, TUN: tailsplit.TUNConfig{Enabled: true}, Rules: tailsplit.RulesConfig{GFWList: "https://ignored.example/rules"},
	}})
	var config tailsplit.Config
	select {
	case config = <-started:
	case <-time.After(time.Second):
		t.Fatal("idle ping was not started")
	}
	if config.TUN.Enabled || config.Rules.GFWList != "" || config.StateDir != runtimeDirectory(server.Base, os.Getuid()) {
		t.Fatalf("idle ping received network/path authority: %+v", config)
	}
	for _, operation := range []string{"ping", "start"} {
		other, response := session(t, path, Request{Protocol: Protocol, Op: operation, Target: "100.64.0.10", Config: &tailsplit.Config{}})
		var reply map[string]any
		if err := response.Decode(&reply); err != nil {
			t.Fatal(err)
		}
		if operation == "ping" && reply["error"] != "ping_busy" || operation == "start" && reply["event"] != "error" {
			t.Fatalf("concurrent %s was not rejected: %v", operation, reply)
		}
		_ = other.Close()
	}
	_ = ping.Close()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("closing the ping connection did not cancel the scoped device")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestExitPingActiveOnlyNeverStartsIdleDevice(t *testing.T) {
	server := &Server{Ping: func(context.Context, tailsplit.Config, string) (tailsplit.ExitPingResult, error) {
		t.Fatal("active-only ping started an idle device")
		return tailsplit.ExitPingResult{}, nil
	}}
	request := Request{Target: "100.64.0.10", ActiveOnly: true, Config: &tailsplit.Config{}}
	if _, err := server.pingExit(context.Background(), request); !errors.Is(err, tailsplit.ErrPingBusy) {
		t.Fatalf("idle active-only ping was not rejected: %v", err)
	}
	server.active.Lock()
	defer server.active.Unlock()
	server.pingLive = func(_ context.Context, id string) (tailsplit.ExitPingResult, error) {
		return tailsplit.ExitPingResult{ID: id}, nil
	}
	if result, err := server.pingExit(context.Background(), request); err != nil || result.ID != request.Target {
		t.Fatalf("active-only ping did not use active engine: %+v %v", result, err)
	}
}

func TestExitPingServiceRejectsTransitionalOrUnsupportedSessions(t *testing.T) {
	server := &Server{}
	request := Request{Target: "100.64.0.10", Config: &tailsplit.Config{}}
	if _, err := server.pingExit(context.Background(), request); !errors.Is(err, tailsplit.ErrPingUnavailable) {
		t.Fatalf("missing capability accepted: %v", err)
	}
	server.Ping = func(context.Context, tailsplit.Config, string) (tailsplit.ExitPingResult, error) {
		t.Fatal("transition started a competing identity")
		return tailsplit.ExitPingResult{}, nil
	}
	server.active.Lock()
	defer server.active.Unlock()
	if _, err := server.pingExit(context.Background(), request); !errors.Is(err, tailsplit.ErrPingBusy) {
		t.Fatalf("login/startup transition not rejected: %v", err)
	}
	request.Target = ""
	if _, err := server.pingExit(context.Background(), request); !errors.Is(err, tailsplit.ErrPingTarget) {
		t.Fatalf("missing target not rejected: %v", err)
	}
}

func TestExitPingServiceJoinsPingBeforeReleasingActiveEngine(t *testing.T) {
	server := &Server{Ping: tailsplit.PingSavedExitNode}
	server.active.Lock()
	defer server.active.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	unregister := RegisterExitPing(server.pingContext(ctx), func(ctx context.Context, _ string) (tailsplit.ExitPingResult, error) {
		close(started)
		<-ctx.Done()
		return tailsplit.ExitPingResult{}, ctx.Err()
	})
	pingDone := make(chan error, 1)
	go func() {
		_, err := server.pingExit(context.Background(), Request{Target: "100.64.0.10"})
		pingDone <- err
	}()
	<-started
	cancel()
	unregister()
	if err := <-pingDone; !errors.Is(err, context.Canceled) {
		t.Fatal("active session cancellation was lost", err)
	}
	if server.pingLive != nil {
		t.Fatal("closed engine still published")
	}
}

func TestExitPingClientCancellationAndErrorCodes(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	canceled := make(chan error, 1)
	go func() { _, err := readPingReply(ctx, client); canceled <- err }()
	cancel()
	select {
	case err := <-canceled:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("ping decoder ignored cancellation")
	}
	client, server = net.Pipe()
	defer client.Close()
	defer server.Close()
	go func() { _ = json.NewEncoder(server).Encode(tailsplit.ExitPingReply{Error: "ping_offline"}) }()
	if _, err := readPingReply(context.Background(), client); !errors.Is(err, tailsplit.ErrPingOffline) {
		t.Fatal(err)
	}
}
