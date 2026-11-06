//go:build darwin

package main

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/idy/tailsplit/internal/tray"
)

func TestConfigurationPanelCanCancelServiceAuthorization(t *testing.T) {
	snapshot := tray.Snapshot{State: "authorizing", Active: true}
	for _, action := range []string{"", "connect", "disconnect", "login", "logout", "switch-network", "refresh-exits", "startup-on", "startup-off"} {
		request := configurationApply{action: action}
		if blocked := request.blocked(false, false, false, snapshot); blocked != (action != "disconnect") {
			t.Fatalf("action %q blocked=%t during service authorization", action, blocked)
		}
	}
	request := configurationApply{action: "disconnect"}
	for _, busy := range [][3]bool{{true, false, false}, {false, true, false}, {false, false, true}} {
		if !request.blocked(busy[0], busy[1], busy[2], snapshot) {
			t.Fatal("disconnect interrupted another owned operation:", busy)
		}
	}
}

func TestAppConfigurationPreservesUserEdits(t *testing.T) {
	directory := t.TempDir()
	template := filepath.Join(directory, "template.yaml")
	path := filepath.Join(directory, "settings", "config.yaml")
	if err := os.WriteFile(template, []byte("listen: 127.0.0.1:17890\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := initializeConfig(path, template); err != nil {
		t.Fatal(err)
	}
	if proxyAddress(path) != "127.0.0.1:17890" {
		t.Fatal("proxy address could not be copied")
	}
	if err := os.WriteFile(path, []byte("listen: 127.0.0.1:19999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := initializeConfig(path, template); err != nil {
		t.Fatal(err)
	}
	if proxyAddress(path) != "127.0.0.1:19999" {
		t.Fatal("app overwrote user configuration")
	}
	stat, _ := os.Stat(path)
	if stat.Mode().Perm() != 0o600 {
		t.Fatal("app config is not private")
	}
}

func TestLegacySharedDeviceNameMigratesWithoutChangingChosenNames(t *testing.T) {
	for name, want := range map[string]string{"letsolo-tailsplit": "", "my-work-mac": "my-work-mac"} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		text := "tailscale:\n  hostname: " + name + "\n  exit_node: 100.64.0.8\ntun:\n  enabled: true\n"
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := initializeConfig(path, "unused-existing-template"); err != nil {
			t.Fatal(err)
		}
		preferences, err := tray.ReadPreferences(path)
		if err != nil || preferences.DeviceName != want || preferences.ExitNode != "100.64.0.8" {
			t.Fatal("legacy-name migration lost a user preference:", preferences, err)
		}
		data, _ := os.ReadFile(path)
		if !bytes.Contains(data, []byte("enabled: true")) {
			t.Fatal("device-name migration changed the selected TUN mode")
		}
	}
}

func TestDesktopLogTailIsBoundedAndValidUTF8(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tailsplit.log")
	if _, err := desktopLogTail(path); !os.IsNotExist(err) {
		t.Fatal("missing log did not report its error:", err)
	}
	if err := os.WriteFile(path, []byte("starting\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if text, err := desktopLogTail(path); err != nil || text != "starting\n" {
		t.Fatal("short log was changed:", text, err)
	}
	data := []byte(strings.Repeat("x", 128<<10) + "\xfflast entry\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	text, err := desktopLogTail(path)
	if err != nil || !utf8.ValidString(text) || !strings.HasSuffix(text, "�last entry\n") || len(text) != (128<<10)+2 {
		t.Fatalf("log tail was not bounded and sanitized: length=%d valid=%t err=%v", len(text), utf8.ValidString(text), err)
	}
}

func TestTemplateIconIsValid(t *testing.T) {
	for _, connected := range []bool{false, true} {
		icon, err := png.Decode(bytes.NewReader(trayIcon(connected)))
		if err != nil || icon.Bounds().Dx() != 32 {
			t.Fatal("invalid tray icon")
		}
	}
}
