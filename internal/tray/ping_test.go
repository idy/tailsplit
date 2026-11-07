package tray

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/pingenv"
)

func TestExitPingHelperProcess(t *testing.T) {
	mode := os.Getenv("TAILSPLIT_PING_HELPER")
	if mode == "" {
		return
	}
	if !slices.Contains(os.Args, "ping") || !slices.Contains(os.Args, "--id") || !slices.Contains(os.Args, "100.64.0.10") {
		os.Exit(4)
	}
	if mode == "clean-env" && (pingenv.HasAuthentication(os.Environ()) || os.Getenv("TAILSPLIT_PING_SENTINEL") != "keep") {
		os.Exit(5)
	}
	if mode == "wait" || mode == "cleanup" {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		if runtime.GOOS == "windows" {
			go func() { var command [1]byte; _, _ = io.ReadFull(os.Stdin, command[:]); cancel() }()
		}
		if mode == "cleanup" {
			if err := os.WriteFile(os.Getenv("TAILSPLIT_PING_STARTED"), []byte("started"), 0o600); err != nil {
				os.Exit(6)
			}
		}
		<-ctx.Done()
		if mode == "cleanup" {
			if err := os.WriteFile(os.Getenv("TAILSPLIT_PING_CANCELED"), []byte("canceled"), 0o600); err != nil {
				os.Exit(7)
			}
			deadline := time.Now().Add(10 * time.Second)
			for {
				if _, err := os.Stat(os.Getenv("TAILSPLIT_PING_RELEASE")); err == nil {
					break
				}
				if time.Now().After(deadline) {
					os.Exit(8)
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
		os.Exit(0)
	}
	reply := tailsplit.ExitPingReply{Result: &tailsplit.ExitPingResult{ID: "100.64.0.10", LatencyMS: 10.25, Direct: true}}
	if mode == "offline" {
		reply = tailsplit.ExitPingReply{Error: "ping_offline"}
	}
	_ = json.NewEncoder(os.Stdout).Encode(reply)
	os.Exit(0)
}

func pingController(t *testing.T, mode string) *Controller {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	controller, err := New(Options{Helper: executable, ConfigPath: filepath.Join(directory, "config.yaml"), LogPath: filepath.Join(directory, "core.log"),
		HelperArgs: []string{"-test.run=^TestExitPingHelperProcess$", "--"}, ExtraEnv: []string{"TAILSPLIT_PING_HELPER=" + mode}})
	if err != nil {
		t.Fatal(err)
	}
	controller.snapshot = Snapshot{State: "stopped", BackendState: "Running", PingAvailable: true,
		ExitNodes: []tailsplit.ExitNode{{ID: "100.64.0.10", Online: true}, {ID: "100.64.0.20"}}}
	t.Cleanup(func() { _ = controller.Close() })
	return controller
}

func TestExitPingControllerValidatesAndPreservesDisconnectedState(t *testing.T) {
	controller := pingController(t, "success")
	for _, test := range []struct {
		id   string
		want error
	}{{"unknown", tailsplit.ErrPingTarget}, {"100.64.0.20", tailsplit.ErrPingOffline}} {
		if _, err := controller.PingExitNode(context.Background(), test.id); !errors.Is(err, test.want) {
			t.Fatal(err)
		}
	}
	result, err := controller.PingExitNode(context.Background(), "100.64.0.10")
	if err != nil || result.LatencyMS != 10.25 || !result.Direct {
		t.Fatalf("bad helper result %+v, %v", result, err)
	}
	if snapshot := controller.Snapshot(); snapshot.Active || snapshot.State != "stopped" || !snapshot.PingAvailable {
		t.Fatalf("ping changed connection state: %+v", snapshot)
	}
	controller = pingController(t, "offline")
	if _, err := controller.PingExitNode(context.Background(), "100.64.0.10"); !errors.Is(err, tailsplit.ErrPingOffline) {
		t.Fatalf("service error lost: %v", err)
	}
}

func TestExitPingHelperDoesNotInheritAuthenticationSettings(t *testing.T) {
	t.Setenv("TS_AUTHKEY", "fixture")
	controller := pingController(t, "clean-env")
	controller.options.ExtraEnv = append(controller.options.ExtraEnv, "TS_AUTH_KEY=fixture", "TSNET_FORCE_LOGIN=1", "TAILSPLIT_PING_SENTINEL=keep")
	if _, err := controller.PingExitNode(context.Background(), "100.64.0.10"); err != nil {
		t.Fatalf("ping helper received implicit login settings: %v", err)
	}
}

func TestWindowsPingHelperWaitsForCleanup(t *testing.T) {
	controller := pingController(t, "cleanup")
	directory := t.TempDir()
	started := filepath.Join(directory, "started")
	canceled := filepath.Join(directory, "canceled")
	release := filepath.Join(directory, "release")
	defer func() { _ = os.WriteFile(release, nil, 0o600) }()
	controller.options.ExtraEnv = append(controller.options.ExtraEnv,
		"TAILSPLIT_PING_STARTED="+started, "TAILSPLIT_PING_CANCELED="+canceled, "TAILSPLIT_PING_RELEASE="+release)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := controller.PingExitNode(ctx, "100.64.0.10"); done <- err }()
	waitForFile := func(path string) {
		t.Helper()
		deadline := time.NewTimer(10 * time.Second)
		defer deadline.Stop()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			if _, err := os.Stat(path); err == nil {
				return
			}
			select {
			case <-deadline.C:
				t.Fatalf("helper did not create %s", filepath.Base(path))
			case <-ticker.C:
			}
		}
	}
	waitForFile(started)
	cancel()
	waitForFile(canceled)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation was lost: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("the caller did not receive cancellation promptly")
	}
	if err := controller.Start(); !errors.Is(err, tailsplit.ErrPingBusy) {
		t.Fatalf("another device operation started during cleanup: %v", err)
	}
	controller.mu.Lock()
	cleanupDone := controller.pingDone
	controller.stopWait = 150 * time.Millisecond
	controller.mu.Unlock()
	if cleanupDone == nil {
		t.Fatal("cleanup was not tracked after the caller returned")
	}
	if err := controller.Stop(); !errors.Is(err, tailsplit.ErrPingBusy) {
		t.Fatalf("stop did not report ongoing cleanup: %v", err)
	}
	if err := controller.Start(); !errors.Is(err, tailsplit.ErrPingBusy) {
		t.Fatalf("timed-out cleanup released the device gate: %v", err)
	}
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cleanupDone:
	case <-time.After(10 * time.Second):
		t.Fatal("helper did not finish after cleanup")
	}
	if err := controller.Stop(); err != nil {
		t.Fatalf("stop failed after cleanup: %v", err)
	}
}

func TestExitPingControllerSerializesStartAndStopsScopedPing(t *testing.T) {
	controller := pingController(t, "wait")
	done := make(chan error, 1)
	go func() { _, err := controller.PingExitNode(context.Background(), "100.64.0.10"); done <- err }()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
wait:
	for {
		controller.mu.Lock()
		started := controller.pingCancel != nil
		controller.mu.Unlock()
		if started {
			break wait
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("ping did not start")
		}
	}
	if err := controller.Start(); !errors.Is(err, tailsplit.ErrPingBusy) {
		t.Fatalf("start competed with scoped ping: %v", err)
	}
	if _, err := controller.PingExitNode(context.Background(), "100.64.0.10"); !errors.Is(err, tailsplit.ErrPingBusy) {
		t.Fatalf("duplicate ping started: %v", err)
	}
	if err := controller.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("stop did not cancel ping: %v", err)
	}
	if !controller.Snapshot().PingAvailable {
		t.Fatal("idle capability was not restored after cancellation")
	}
}

func TestExitPingControllerCapabilityTracksDeviceAndTransitions(t *testing.T) {
	controller := pingController(t, "success")
	controller.command = &exec.Cmd{}
	for _, state := range []string{"starting", "checking-exit", "authenticating", "discovering", "authorizing", "stopping"} {
		controller.snapshot.State = state
		controller.snapshot.Mode = "proxy"
		if controller.Snapshot().PingAvailable {
			t.Fatalf("ping available during %s", state)
		}
	}
	controller.snapshot.State = "running"
	want := runtime.GOOS == "darwin" || runtime.GOOS == "windows"
	if controller.Snapshot().PingAvailable != want {
		t.Fatal("capability does not match service transport")
	}
	controller.command = nil
	controller.snapshot.BackendState = "NeedsLogin"
	if controller.Snapshot().PingAvailable {
		t.Fatal("signed-out device advertises ping")
	}
}
