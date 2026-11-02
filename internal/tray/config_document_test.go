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
	if err := SaveConfigDocument(path, newRevision, []byte("listen: 0.0.0.0:80\n")); err == nil {
		t.Fatal("invalid configuration was saved")
	}
	actual, _, _ = ReadConfigDocument(path)
	if string(actual) != string(updated) {
		t.Fatal("failed save modified the current document")
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
