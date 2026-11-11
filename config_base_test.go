package tailsplit

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCapturedConfigurationWithOriginalBase(t *testing.T) {
	base, snapshotDir := t.TempDir(), t.TempDir()
	path := filepath.Join(snapshotDir, "captured.yaml")
	source := []byte("state_dir: ./profile\nrules:\n  source: ./policy.txt\n  default_exit: \"-\"\n")
	if err := os.WriteFile(path, source, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "policy.txt"), []byte("example.com\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for name, load := range map[string]func(string, string) (Config, error){"saved": LoadWithBase, "draft": LoadDraftWithBase} {
		t.Run(name, func(t *testing.T) {
			config, err := load(path, base)
			if err != nil {
				t.Fatal(err)
			}
			if config.StateDir != filepath.Join(base, "profile") || config.Rules.GFWList != filepath.Join(base, "policy.txt") {
				t.Fatal("relocated source changed relative-path semantics", config.StateDir, config.Rules.GFWList)
			}
			ordinary, err := load(path, "")
			if err != nil || ordinary.StateDir != filepath.Join(snapshotDir, "profile") {
				t.Fatal("empty base changed ordinary loading", ordinary.StateDir, err)
			}
		})
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, source) {
		t.Fatal("loading rewrote captured source", err)
	}
}

func TestLoadWithBaseRetainsSchemaAndSizeBounds(t *testing.T) {
	for name, load := range map[string]func(string, string) (Config, error){"saved": LoadWithBase, "draft": LoadDraftWithBase} {
		for label, source := range map[string][]byte{
			"oversize": bytes.Repeat([]byte(" "), (1<<20)+1),
			"unknown":  []byte("unknown_setting: true\n"),
			"multiple": []byte("version: 1\n---\nversion: 1\n"),
		} {
			t.Run(name+"/"+label, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "captured.yaml")
				if err := os.WriteFile(path, source, 0600); err != nil {
					t.Fatal(err)
				}
				if _, err := load(path, t.TempDir()); err == nil {
					t.Fatal("explicit base bypassed validation", label)
				}
			})
		}
	}
}

func TestDraftWithOriginalBaseDoesNotClaimSavedRuleCache(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(t.TempDir(), "captured.yaml")
	source := []byte("state_dir: ./profile\nrules:\n  source: https://rules.example/list.txt\n  default_exit: \"-\"\n")
	if err := os.WriteFile(path, source, 0600); err != nil {
		t.Fatal(err)
	}
	saved, err := LoadWithBase(path, base)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := LoadDraftWithBase(path, base)
	if err != nil {
		t.Fatal(err)
	}
	if saved.rulesCacheStateDir != saved.StateDir || saved.rulesCacheSource == "" {
		t.Fatal("saved cache ownership lost original base")
	}
	if draft.rulesCacheSource != "" || draft.rulesCacheStateDir != "" {
		t.Fatal("draft gained saved cache ownership")
	}
}
