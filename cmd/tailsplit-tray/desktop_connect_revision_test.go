package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/idy/tailsplit/internal/tray"
)

func TestDesktopConnectRejectsChangedConfigBeforeLoginAndDeferredStart(t *testing.T) {
	for _, phase := range []string{"before-login", "before-queue", "before-start", "unchanged"} {
		t.Run(phase, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte("rules:\n  default_exit: office\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			_, revision, err := tray.ReadConfigDocument(path)
			if err != nil {
				t.Fatal(err)
			}
			change := func() {
				if err := os.WriteFile(path, []byte("rules:\n  default_exit: another\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			controller := &desktopConnectFixture{trafficExit: "office"}
			if phase == "before-login" {
				controller.snapshot.Exits = map[string]tray.ExitSnapshot{"office": {BackendState: "NeedsLogin"}}
			}
			if phase == "before-login" || phase == "before-queue" {
				change()
			}
			var flow tray.LoginFlow
			operation, err := checkedDesktopConnectOperation(path, revision, controller, &flow)
			if phase == "before-login" || phase == "before-queue" {
				if !errors.Is(err, tray.ErrConfigChanged) || operation != nil || len(controller.calls) != 0 {
					t.Fatal("stale click reached networking", err, controller.calls)
				}
				return
			}
			if err != nil || operation == nil {
				t.Fatal("fresh click rejected", err)
			}
			if phase == "before-start" {
				change()
			}
			err = operation()
			if phase == "before-start" {
				if !errors.Is(err, tray.ErrConfigChanged) || len(controller.calls) != 0 {
					t.Fatal("queued start used changed settings", err, controller.calls)
				}
			} else if err != nil || len(controller.calls) != 1 || controller.calls[0] != "connect" {
				t.Fatal("fresh settings failed to connect", err, controller.calls)
			}
		})
	}
}
