package tray

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idy/tailsplit"
)

func TestRepairPreservesPolicyAndBacksUpOriginal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := "# Keep my policy\nversion: 1\nstate_dir: ./custom-state\nlisten: 127.0.0.1:17891\ntailscale:\n  hostname: my-device\n  exit_node: 100.64.0.10\nexits:\n  - id: office\n    name: Office\n    exit_node: 100.101.0.11\n    tailnet: work.example\ndns:\n  listen: 127.0.0.1:53\n  proxy: 8.8.8.8 # Keep upstream\n  direct_ipv6: false\nrules:\n  default_exit: office\n  proxy_domains: [private.example]\n  routes:\n    - exit: office\n      domains: [work.example]\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	_, revision, _ := ReadConfigDocument(path)
	backup, err := RepairConfigDocument(path, revision)
	if err != nil {
		t.Fatal(err)
	}
	saved, _, _ := ReadConfigDocument(path)
	backedUp, err := os.ReadFile(backup)
	if err != nil || string(backedUp) != original {
		t.Fatalf("backup differs: %v", err)
	}
	for _, retained := range []string{"# Keep my policy", "# Keep upstream", "./custom-state", "127.0.0.1:17891", "work.example", "private.example"} {
		if !strings.Contains(string(saved), retained) {
			t.Fatalf("lost %q: %s", retained, saved)
		}
	}
	config, err := tailsplit.Load(path)
	if err != nil || config.Tailscale.Hostname != "my-device" || len(config.Exits) != 1 || config.Exits[0].ID != "office" || config.Rules.DefaultExit != "office" || len(config.Rules.Routes) != 1 {
		t.Fatalf("policy changed: %+v %v", config, err)
	}
	if strings.Contains(string(saved), "listen: 127.0.0.1:53") {
		t.Fatal("obsolete listener retained")
	}
}

func TestRepairRefusesAmbiguousOrUnrelatedErrors(t *testing.T) {
	for _, original := range []string{
		"dns: [\n", "version: 1\n", "dns:\n  lissten: x\n", "dns:\n  listen: x\n  proxy: invalid-server\n",
		"dns:\n  listen: x\n  unknown: x\n", "dns:\n  listen: x\n  listen: y\n", "dns:\n  listen: x\nunknown: true\n",
		"dns:\n  listen: x\n---\nversion: 1\n", "dns: &dns\n  <<: &defaults {listen: x}\n",
		"dns:\n  listen: x\nrules:\n  default_exit: missing\n",
	} {
		t.Run(original, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			_, revision, _ := ReadConfigDocument(path)
			if _, err := RepairConfigDocument(path, revision); !errors.Is(err, ErrConfigNotRepairable) {
				t.Fatalf("unexpected repair: %v", err)
			}
			current, _, _ := ReadConfigDocument(path)
			if string(current) != original {
				t.Fatal("failed repair changed source")
			}
			backups, _ := filepath.Glob(path + ".backup-*")
			if len(backups) != 0 {
				t.Fatal("invalid repair created backup")
			}
		})
	}
}

func TestRepairPreservesAliasesOfRemovedValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := []byte("dns:\n  listen: &resolver 8.8.8.8\n  proxy: *resolver\n")
	candidate, err := PrepareConfigRepair(path, original)
	if err != nil {
		t.Fatal(err)
	}
	config, err := ValidateConfigDocument(path, candidate)
	if err != nil || config.DNS.Proxy != "8.8.8.8" {
		t.Fatalf("alias lost: %s %v", candidate, err)
	}
}

func TestRepairChecksRevisionBeforeBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := []byte("dns:\n  listen: x\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	for _, revision := range []string{"", "stale"} {
		if _, err := RepairConfigDocument(path, revision); !errors.Is(err, ErrConfigChanged) {
			t.Fatalf("revision not checked: %v", err)
		}
	}
	backups, _ := filepath.Glob(path + ".backup-*")
	if len(backups) != 0 {
		t.Fatal("stale request made backup")
	}
}
