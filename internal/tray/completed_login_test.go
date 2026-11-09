package tray

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/idy/tailsplit"
)

func TestSnapshotLoginOwnerDoesNotFollowLaterSavedPreferences(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "config.yaml")
	if err := os.WriteFile(path, []byte("tailscale: {exit_node: 100.64.0.10}\nrules: {default_exit: main}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	controller, err := New(Options{Helper: "unused", ConfigPath: path, LogPath: filepath.Join(directory, "core.log")})
	if err != nil {
		t.Fatal(err)
	}
	defer controller.log.Close()
	// A live core still belongs to office after an external config edit selects main.
	controller.command, controller.trafficExit = &exec.Cmd{}, "office"
	controller.snapshot = Snapshot{State: "running", Mode: "tun", BackendState: "NeedsLogin",
		Exits: map[string]ExitSnapshot{"office": {BackendState: "Running"}}}
	if saved, err := controller.TrafficExitID(); err != nil || saved != tailsplit.MainExit {
		t.Fatal("fixture did not change the saved preference", saved, err)
	}
	if snapshot := controller.Snapshot(); !snapshot.Active || snapshot.TrafficExit != "office" || snapshot.LoginExitID() != "office" {
		t.Fatal("saved policy replaced the running helper's authorization owner", snapshot)
	}
}

func TestCompletedExitLoginHelperProcess(t *testing.T) {
	if os.Getenv("TAILSPLIT_COMPLETED_LOGIN_HELPER") != "1" {
		return
	}
	fmt.Println(`{"event":"status","exit":"office","backend_state":"Running","network_name":"office.ts.net"}`)
	fmt.Println(`{"event":"authenticated","mode":"login"}`)
	os.Exit(0)
}

func TestCompletedExitLoginResolvesAfterHelperExitsBeforePolling(t *testing.T) {
	for _, primary := range []string{"", "NeedsLogin", "Direct"} {
		t.Run("primary="+primary, func(t *testing.T) {
			controller := testController(t, "manual-events")
			controller.options.HelperArgs = []string{"-test.run=^TestCompletedExitLoginHelperProcess$", "--"}
			controller.options.ExtraEnv = append(controller.options.ExtraEnv, "TAILSPLIT_COMPLETED_LOGIN_HELPER=1")
			controller.snapshot = Snapshot{State: "stopped", BackendState: primary,
				Exits: map[string]ExitSnapshot{"lab": {BackendState: "Running", NetworkName: "lab.ts.net"}}}
			if err := controller.StartExitLogin("office"); err != nil {
				t.Fatal(err)
			}
			var flow LoginFlow
			flow.Request()
			// Deliberately avoid LoginFlow.Next until Wait and cleanup finish.
			controller.mu.Lock()
			done := controller.done
			controller.mu.Unlock()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("saved-login helper did not exit")
			}
			snapshot := controller.Snapshot()
			if snapshot.Active || snapshot.State != "authenticated" || snapshot.Mode != "login" || snapshot.Focus != "" || snapshot.LoginURL != "" ||
				snapshot.BackendState != primary || snapshot.Exits["office"].BackendState != "Running" || snapshot.Exits["lab"].NetworkName != "lab.ts.net" {
				t.Fatal("helper completion did not preserve the target's successful result", snapshot)
			}
			if link := flow.Next(snapshot); link != "" || flow.pending || flow.Opening() || flow.ShowMessage() || flow.Message() != "设备已登录" {
				t.Fatal("successful focused login remained pending after its helper exited", flow.Message())
			}
			if flow.Next(snapshot) != "" {
				t.Fatal("completed saved login opened a browser")
			}
			// A later login still waits for its own helper instead of reusing success.
			flow.Request()
			snapshot.State, snapshot.Focus, snapshot.BackendState = "starting", tailsplit.MainExit, ""
			if flow.Next(snapshot) != "" || !flow.pending {
				t.Fatal("a new primary request reused another device's completed login")
			}
		})
	}
}
