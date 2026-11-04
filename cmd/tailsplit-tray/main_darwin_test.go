//go:build darwin

package main

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/idy/tailsplit/internal/tray"
)

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

func TestTemplateIconIsValid(t *testing.T) {
	for _, connected := range []bool{false, true} {
		icon, err := png.Decode(bytes.NewReader(trayIcon(connected)))
		if err != nil || icon.Bounds().Dx() != 32 {
			t.Fatal("invalid tray icon")
		}
	}
}
