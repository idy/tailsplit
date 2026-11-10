package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/idy/tailsplit/internal/tray"
)

func TestDesktopExitLoginPreservesRevisionConflict(t *testing.T) {
	for _, failure := range []error{tray.ErrConfigChanged, fmt.Errorf("private-helper-diagnostic: %w", tray.ErrConfigChanged)} {
		var flow tray.LoginFlow
		flow.Request()
		err := finishDesktopExitLogin(&flow, failure)
		if !errors.Is(err, tray.ErrConfigChanged) {
			t.Fatalf("revision conflict was replaced: %v", err)
		}
		if strings.Contains(err.Error(), "private-helper-diagnostic") {
			t.Fatal("wrapped diagnostic escaped sanitization", err)
		}
		if got := tray.Translate("en", flow.Message()); got != "The configuration changed. Reload it and try again." {
			t.Fatal("revision conflict lost its localized flow message", got)
		}
		if flow.Next(tray.Snapshot{State: "login", BackendState: "NeedsLogin", LoginURL: "https://login.tailscale.com/a/stale-fixture"}) != "" {
			t.Fatal("failed Add retained browser intent")
		}
	}
}

func TestDesktopExitLoginStillSanitizesOtherHelperErrors(t *testing.T) {
	var flow tray.LoginFlow
	err := finishDesktopExitLogin(&flow, errors.New("private-helper-diagnostic"))
	if err == nil || strings.Contains(err.Error(), "private-helper-diagnostic") {
		t.Fatal("helper diagnostic escaped sanitization", err)
	}
}
