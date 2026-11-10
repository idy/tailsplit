package tray

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/privatefs"
	"gopkg.in/yaml.v3"
)

func exitAdditionDocument(t *testing.T, data string) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	_, revision, err := ReadConfigDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, revision
}

func TestPrepareExitAdditionPreservesRawYAMLAndRetainedPolicy(t *testing.T) {
	original := "# Preserve document comments\nversion: 1\nstate_dir: ./relative-state\ntailscale:\n  hostname: fixture-main\n  name: &label 'Shared label' # Anchor comment\nexits:\n  - id: saved # Saved device\n    name: *label # Alias comment\n    tailnet: saved.ts.net\nrules:\n  source: ''\n  default_exit: main\n  global_exit: saved\n  final: retired\n  routes:\n    - exit: retired-route # Retained group\n      domains: [retained.example]\n"
	path, revision := exitAdditionDocument(t, original)
	candidate, actual, err := PrepareExitAddition(path, "new-exit", revision)
	if err != nil || actual != revision {
		t.Fatal("could not prepare additional exit", actual, err)
	}
	for _, preserved := range []string{"# Preserve document comments", "&label 'Shared label' # Anchor comment", "name: *label # Alias comment", "id: saved # Saved device", "state_dir: ./relative-state", "exit: retired-route # Retained group", "domains: [retained.example]"} {
		if !strings.Contains(string(candidate), preserved) {
			t.Errorf("candidate lost source YAML %q\n%s", preserved, candidate)
		}
	}
	config, err := ValidateConfigDocument(path, candidate)
	if err != nil || len(config.Exits) != 2 || config.Exits[1].ID != "new-exit" || !config.Exits[1].AutoName || config.Exits[1].Name != "" || config.Exits[1].ExitNode != "" {
		t.Fatal("candidate did not append one automatically named pending device", config.Exits, err)
	}
	if config.StateDir != filepath.Join(filepath.Dir(path), "relative-state") || config.Rules.DefaultExit != "main" || config.Rules.GlobalExit != "saved" || config.Rules.Final != "retired" || config.Rules.Routes[0].Exit != "retired-route" {
		t.Fatal("addition changed relative paths or retained policy")
	}
	var raw struct {
		Exits []tailsplit.ExitConfig `yaml:"exits"`
	}
	if err := yaml.Unmarshal(candidate, &raw); err != nil || raw.Exits[0].Hostname != "" || raw.Exits[1].Hostname != "" {
		t.Fatal("addition wrote normalized implicit hostnames into the YAML", err)
	}
	if saved, err := os.ReadFile(path); err != nil || !bytes.Equal(saved, []byte(original)) {
		t.Fatal("preparing an addition modified the saved document", err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatal("preparing an addition left staged state or configuration files", entries, err)
	}
}

func TestRetainedConfigCandidateIsPrivateAndKeepsRelativePaths(t *testing.T) {
	path, _ := exitAdditionDocument(t, "state_dir: ./saved-state\n")
	data := []byte("state_dir: ./pending-state\ntailscale: null\nexits: [{id: new-exit, auto_name: true}]\n")
	candidate, config, err := configCandidate(path, data)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(candidate)
	if filepath.Dir(candidate) != filepath.Dir(path) || config.StateDir != filepath.Join(filepath.Dir(path), "pending-state") {
		t.Fatal("candidate changed relative-path resolution")
	}
	if err := privatefs.CheckFile(candidate); err != nil {
		t.Fatal("validated candidate does not satisfy private-file policy", err)
	}
	if content, err := os.ReadFile(candidate); err != nil || !bytes.Equal(content, data) {
		t.Fatal("retained candidate differs from validated bytes", err)
	}
}

func TestPrepareExitAdditionChecksRevisionBeforeValidationOrStaging(t *testing.T) {
	// The document is deliberately invalid. A stale revision must win before
	// validation creates any candidate, including on an unreadable draft schema.
	path, _ := exitAdditionDocument(t, "unknown_field: true\n")
	candidate, revision, err := PrepareExitAddition(path, "new-exit", "stale-revision")
	if !errors.Is(err, ErrConfigChanged) || candidate != nil || revision != "" {
		t.Fatal("stale request reached candidate validation", revision, err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatal("stale request staged a configuration", entries, err)
	}
}

func TestPrepareExitAdditionEmptyInventoryLeavesBothSelectorsUnset(t *testing.T) {
	path, current := exitAdditionDocument(t, "version: 1\nstate_dir: ./state\ntailscale: null\nexits: []\nrules: {source: '', final: retired, routes: [{exit: retained, domains: [retained.example]}]}\n")
	for _, expected := range []string{"", current} {
		candidate, revision, err := PrepareExitAddition(path, "new-exit", expected)
		if err != nil || revision != current {
			t.Fatal(err)
		}
		config, err := ValidateConfigDocument(path, candidate)
		if err != nil || config.Rules.DefaultExit != tailsplit.UnsetExit || config.Rules.GlobalExit != tailsplit.UnsetExit || config.Rules.Final != "retired" || config.Rules.Routes[0].Exit != "retained" {
			t.Fatal("first added device inherited traffic or discarded retained policy", err)
		}
	}
}

func TestPrepareExitAdditionRejectsReservedIDsPoliciesAndLimit(t *testing.T) {
	base := "state_dir: ./state\ntailscale: null\nexits: []\nrules: {source: '', default_exit: '-', global_exit: '-'}\n"
	for _, id := range []string{"", "main", "proxy", "direct", "con", "bad/exit", strings.Repeat("a", 33)} {
		path, revision := exitAdditionDocument(t, base)
		if _, _, err := PrepareExitAddition(path, id, revision); err == nil {
			t.Error("invalid additional ID accepted", id)
		}
	}
	for _, policy := range []string{
		"final: retained", "default_exit: retained", "global_exit: retained", "routes: [{exit: retained, domains: [retained.example]}]",
	} {
		path, revision := exitAdditionDocument(t, "state_dir: ./state\ntailscale: null\nexits: []\nrules: {source: '', "+policy+"}\n")
		if _, _, err := PrepareExitAddition(path, "retained", revision); err == nil {
			t.Error("addition rebound a retained policy owner", policy)
		} else if (strings.HasPrefix(policy, "final:") || strings.HasPrefix(policy, "routes:")) && !strings.Contains(err.Error(), "retained routing policy") {
			t.Error("addition did not identify a retained policy owner", policy, err)
		}
	}
	for _, count := range []int{1, 8} {
		var document strings.Builder
		document.WriteString("state_dir: ./state\ntailscale: null\nrules: {source: '', default_exit: '-', global_exit: '-'}\nexits:\n")
		for index := 0; index < count; index++ {
			fmt.Fprintf(&document, "  - id: exit-%d\n", index)
		}
		path, revision := exitAdditionDocument(t, document.String())
		id := "exit-0"
		if count == 8 {
			id = "new-exit"
		}
		if _, _, err := PrepareExitAddition(path, id, revision); err == nil {
			t.Error("duplicate or ninth additional device accepted", count)
		}
	}
}
