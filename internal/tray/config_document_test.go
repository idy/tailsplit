package tray

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigDocumentSaveChecksRevisionAndRetainsLiteralYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	initial := "# User formatting\nlisten: '127.0.0.1:17890'\n"
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	_, revision, err := ReadConfigDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := []byte("# Deliberate raw formatting\nlisten: \"127.0.0.1:17892\"\n\n")
	if err := SaveConfigDocument(path, revision, updated); err != nil {
		t.Fatal(err)
	}
	actual, newRevision, err := ReadConfigDocument(path)
	if err != nil || string(actual) != string(updated) || newRevision == revision {
		t.Fatal("raw edit did not retain its exact text:", string(actual), err)
	}
	if err := SaveConfigDocument(path, revision, []byte(initial)); !errors.Is(err, ErrConfigChanged) {
		t.Fatal("stale editor overwrote a new configuration:", err)
	}
	for _, invalid := range []string{"listen: 0.0.0.0:80\n", "dns:\n  listen: 127.0.0.1:15353\n"} {
		if err := SaveConfigDocument(path, newRevision, []byte(invalid)); err == nil {
			t.Fatalf("invalid configuration was saved: %s", invalid)
		}
	}
	actual, _, _ = ReadConfigDocument(path)
	if string(actual) != string(updated) {
		t.Fatal("failed save modified the current document")
	}
}

func TestRuleSourcePatchOverridesMergedLegacyValues(t *testing.T) {
	for name, rules := range map[string]string{
		"inline merge":    "  <<: {gfwlist: builtin, direct_domains: [example.com]}\n",
		"anchored merge":  "  <<: &defaults\n    gfwlist: builtin # Keep inherited source\n    direct_domains: [example.com]\n",
		"merge sequence":  "  <<: [{gfwlist: builtin}, {direct_domains: [example.com]}]\n",
		"direct override": "  <<: {gfwlist: builtin, direct_domains: [example.com]}\n  gfwlist: https://rules.example/old.txt\n",
	} {
		t.Run(name, func(t *testing.T) {
			original := []byte("tailscale: {}\nrules:\n" + rules)
			path := filepath.Join(t.TempDir(), "config.yaml")
			if _, err := ValidateConfigDocument(path, original); err != nil {
				t.Fatal("invalid legacy fixture:", err)
			}
			for _, source := range []string{"https://rules.example/new.txt", "builtin", ""} {
				updated, err := PatchConfigDocument(original, ConfigChange{"rules", "source", source})
				if err != nil {
					t.Fatal(err)
				}
				config, err := ValidateConfigDocument(path, updated)
				if err != nil || config.Rules.GFWList != source || len(config.Rules.DirectDomains) != 1 || config.Rules.DirectDomains[0] != "example.com" {
					t.Fatalf("merged source edit failed: %+v, %v\n%s", config.Rules, err, updated)
				}
				if !strings.Contains(string(updated), "<<:") || name == "anchored merge" && (!strings.Contains(string(updated), "&defaults") || !strings.Contains(string(updated), "# Keep inherited source")) {
					t.Fatalf("source edit replaced the merge or its comments: %s", updated)
				}
			}
		})
	}
}

func TestConfigValidationKeepsRelativePathsAndCleansTemporaryFiles(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "config.yaml")
	config, err := ValidateConfigDocument(path, []byte("state_dir: ./my-state\n"))
	if err != nil || config.StateDir != filepath.Join(directory, "my-state") {
		t.Fatal("validation changed the base for relative paths:", config.StateDir, err)
	}
	if _, err := ValidateConfigDocument(path, []byte("unknown: true\n")); err == nil {
		t.Fatal("unknown field was accepted")
	}
	files, _ := os.ReadDir(directory)
	if len(files) != 0 {
		t.Fatal("validation left configuration candidates on disk")
	}
	if _, err := PatchConfigDocument([]byte(strings.Repeat("x", MaxConfigBytes+1))); err == nil {
		t.Fatal("oversized preview accepted")
	}
}
