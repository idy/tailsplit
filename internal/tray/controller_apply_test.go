package tray

import (
	"errors"
	"testing"
	"time"

	"github.com/idy/tailsplit"
)

func TestCheckedReconfigureUsesCurrentSessionBeforeStopping(t *testing.T) {
	controller := testController(t, "normal")
	// An HTTP request can observe this inactive snapshot before tray Connect
	// acquires the controller. The checked apply must use the newer session.
	if controller.Snapshot().Active {
		t.Fatal("fixture must begin disconnected")
	}
	if err := controller.Start(); err != nil {
		t.Fatal(err)
	}
	waitState(t, controller, "running")
	controller.mu.Lock()
	command := controller.command
	controller.mu.Unlock()
	rejected := errors.New("candidate is an incomplete draft")
	updated := false
	err := controller.ReconfigureChecked(func(snapshot Snapshot) error {
		if !snapshot.Active || snapshot.Mode != "proxy" {
			t.Fatalf("checked apply did not observe concurrent Connect: %+v", snapshot)
		}
		return rejected
	}, func() error {
		updated = true
		return nil
	})
	controller.mu.Lock()
	unchanged := controller.command == command && !controller.stopping
	controller.mu.Unlock()
	if !errors.Is(err, rejected) || updated || !unchanged || !controller.Snapshot().Active {
		t.Fatalf("rejected candidate stopped or replaced the current helper: %v, updated=%v, unchanged=%v", err, updated, unchanged)
	}
}

func TestCheckedReconfigureBlocksPublicStartsUntilApplyFinishes(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(map[bool]string{false: "idle", true: "reconnect"}[active], func(t *testing.T) {
			controller := testController(t, "normal")
			if active {
				if err := controller.Start(); err != nil {
					t.Fatal(err)
				}
				waitState(t, controller, "running")
			}
			checked, releaseCheck := make(chan struct{}), make(chan struct{})
			updated, releaseUpdate := make(chan struct{}), make(chan struct{})
			done := make(chan error, 1)
			go func() {
				done <- controller.ReconfigureChecked(func(snapshot Snapshot) error {
					close(checked)
					<-releaseCheck
					return nil
				}, func() error {
					close(updated)
					<-releaseUpdate
					return nil
				})
			}()
			releasedCheck, releasedUpdate := false, false
			defer func() {
				if !releasedCheck {
					close(releaseCheck)
				}
				if !releasedUpdate {
					close(releaseUpdate)
				}
			}()
			assertStartsBlocked := func() {
				t.Helper()
				for name, start := range map[string]func() error{
					"connect": controller.Start, "login": controller.StartLogin,
					"exit login": func() error { return controller.StartExitLogin("office") },
					"restart":    controller.Restart,
				} {
					if err := start(); !errors.Is(err, tailsplit.ErrPingBusy) {
						t.Fatalf("%s entered during checked apply: %v", name, err)
					}
				}
			}
			select {
			case <-checked:
			case <-time.After(5 * time.Second):
				t.Fatal("candidate check did not start")
			}
			assertStartsBlocked()
			close(releaseCheck)
			releasedCheck = true
			select {
			case <-updated:
			case <-time.After(5 * time.Second):
				t.Fatal("configuration update did not start")
			}
			assertStartsBlocked()
			if err := controller.RefreshExitNodes(); !errors.Is(err, tailsplit.ErrPingBusy) {
				t.Fatalf("discovery entered during the stopped update gap: %v", err)
			}
			close(releaseUpdate)
			releasedUpdate = true
			select {
			case err := <-done:
				if err != nil {
					t.Fatal("internal reconnect was blocked by its own gate:", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("checked apply did not finish")
			}
			if active {
				waitState(t, controller, "running")
			} else if controller.Snapshot().Active {
				t.Fatal("an idle save started a helper")
			}
		})
	}
}
