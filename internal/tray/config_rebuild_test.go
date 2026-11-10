package tray

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/privatefs"
)

func writeRebuildDocument(t *testing.T, path string, data []byte) string {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	_, revision, err := ReadConfigDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	return revision
}

func requireRebuildBytes(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s does not contain the exact expected bytes:\n%q\nwant:\n%q", path, got, want)
	}
}

func TestRebuildConfigDocumentReplacesInvalidConfigWithDesktopDefaults(t *testing.T) {
	for name, original := range map[string][]byte{
		"bad YAML":           []byte("# Preserve original bytes\r\nversion: 1\r\ndns: [\r\n\r\n"),
		"unknown dns.listen": []byte("# Removed schema field\nversion: 1\ndns:\n  listen: '127.0.0.1:15353' # Keep this in backup\n\n"),
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			revision := writeRebuildDocument(t, path, original)
			if _, err := tailsplit.LoadDraft(path); err == nil {
				t.Fatal("fixture must be invalid")
			}

			backupPath, err := RebuildConfigDocument(path, revision)
			if err != nil {
				t.Fatal(err)
			}
			if filepath.Dir(backupPath) != filepath.Dir(path) {
				t.Fatal("backup is not beside the configuration:", backupPath)
			}
			prefix := filepath.Base(path) + ".backup-"
			if !strings.HasPrefix(filepath.Base(backupPath), prefix) {
				t.Fatal("backup does not have the configuration backup prefix:", backupPath)
			}
			stamp, unique, ok := strings.Cut(strings.TrimPrefix(filepath.Base(backupPath), prefix), "-")
			if _, err := time.Parse("20060102T150405Z", stamp); err != nil || !ok || unique == "" {
				t.Fatal("backup name must include a timestamp and unique suffix:", backupPath)
			}
			requireRebuildBytes(t, backupPath, original)
			requireRebuildBytes(t, path, tailsplit.DefaultYAML)
			for _, privatePath := range []string{backupPath, path} {
				if err := privatefs.CheckFile(privatePath); err != nil {
					t.Fatalf("%s does not satisfy private-file policy: %v", privatePath, err)
				}
			}
			config, err := tailsplit.LoadDraft(path)
			if err != nil {
				t.Fatal("rebuilt desktop defaults do not load:", err)
			}
			if config.Tailscale != nil || len(config.Exits) != 0 || len(config.ExitIDs()) != 0 {
				t.Fatal("rebuilt configuration contains device identities")
			}
			if config.Rules.DefaultExit != tailsplit.UnsetExit || config.Rules.GlobalExit != tailsplit.UnsetExit {
				t.Fatal("rebuilt configuration has a selected gateway")
			}
			entries, err := os.ReadDir(filepath.Dir(path))
			if err != nil || len(entries) != 2 {
				t.Fatal("rebuild left files other than the config and backup:", entries, err)
			}
		})
	}
}

func TestRebuildConfigDocumentRequiresCurrentNonemptyRevision(t *testing.T) {
	for _, name := range []string{"empty", "stale"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			original := []byte("dns: [\n")
			revision := writeRebuildDocument(t, path, original)
			if name == "empty" {
				revision = ""
			} else {
				original = []byte("# A newer edit must survive\ndns:\n  listen: 127.0.0.1:15353\n")
				writeRebuildDocument(t, path, original)
			}

			backupPath, err := RebuildConfigDocument(path, revision)
			if !errors.Is(err, ErrConfigChanged) || backupPath != "" {
				t.Fatalf("revision rejection = (%q, %v), want no backup and ErrConfigChanged", backupPath, err)
			}
			requireRebuildBytes(t, path, original)
			entries, err := os.ReadDir(filepath.Dir(path))
			if err != nil || len(entries) != 1 {
				t.Fatal("rejected revision created a backup or candidate:", entries, err)
			}
		})
	}
}

func TestRebuildConfigDocumentRetainsDistinctBackups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := []byte("# First broken configuration\ntailscale: [\n")
	revision := writeRebuildDocument(t, path, original)
	oldBackup := path + ".backup-20000101T000000Z-existing"
	oldBytes := []byte("# Retain the older recovery copy\n")
	if err := os.WriteFile(oldBackup, oldBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	backups := map[string][]byte{oldBackup: oldBytes}
	// Later calls also prove that validity checks belong to the HTTP API:
	// rebuilding a valid default document is allowed by this storage function.
	for range 3 {
		backupPath, err := RebuildConfigDocument(path, revision)
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := backups[backupPath]; exists {
			t.Fatal("rebuild reused an existing backup path:", backupPath)
		}
		backups[backupPath] = original
		original, revision, err = ReadConfigDocument(path)
		if err != nil {
			t.Fatal(err)
		}
	}
	for backupPath, want := range backups {
		requireRebuildBytes(t, backupPath, want)
	}
	requireRebuildBytes(t, path, tailsplit.DefaultYAML)
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != len(backups)+1 {
		t.Fatal("rebuild lost a backup or left a candidate:", entries, err)
	}
}

func TestRebuildConfigDocumentBackupFailureLeavesOriginal(t *testing.T) {
	// The original basename fits the filesystem's component limit; adding the
	// backup suffix does not. This also works with elevated filesystem access.
	path := filepath.Join(t.TempDir(), strings.Repeat("c", 240)+".yaml")
	original := []byte("dns: [\n")
	revision := writeRebuildDocument(t, path, original)
	backupPath, err := RebuildConfigDocument(path, revision)
	if err == nil || backupPath != "" {
		t.Fatalf("backup creation failure = (%q, %v), want no backup and an error", backupPath, err)
	}
	requireRebuildBytes(t, path, original)
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatal("backup failure left an extra file:", entries, err)
	}
}

func TestRebuildConfigDocumentSaveFailureRetainsBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := []byte("# Original recovery bytes\ndns: [\n")
	revision := writeRebuildDocument(t, path, original)
	// Force candidate validation to fail after a complete backup. This test is
	// sequential because the embedded default is shared with other tray tests.
	defaults := tailsplit.DefaultYAML
	tailsplit.DefaultYAML = []byte("dns:\n  listen: 127.0.0.1:15353\n")
	t.Cleanup(func() { tailsplit.DefaultYAML = defaults })

	backupPath, err := RebuildConfigDocument(path, revision)
	if err == nil || backupPath == "" {
		t.Fatalf("save failure = (%q, %v), want a retained backup path and an error", backupPath, err)
	}
	requireRebuildBytes(t, path, original)
	requireRebuildBytes(t, backupPath, original)
	if err := privatefs.CheckFile(backupPath); err != nil {
		t.Fatal("retained backup does not satisfy private-file policy:", err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 2 {
		t.Fatal("save failure lost the backup or left a candidate:", entries, err)
	}
}

func TestRebuildConfigDocumentReadFailureDoesNotCreateBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	backupPath, err := RebuildConfigDocument(path, "expected-revision")
	if !errors.Is(err, os.ErrNotExist) || backupPath != "" {
		t.Fatalf("missing config = (%q, %v), want no backup and ErrNotExist", backupPath, err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 0 {
		t.Fatal("read failure created a file:", entries, err)
	}
}
