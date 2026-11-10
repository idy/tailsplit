package tray

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/idy/tailsplit/internal/privatefs"
)

var connectionSnapshotConfig = []byte("version: 1\nstate_dir: ./state\ntailscale: null\nexits: []\nrules:\n  source: ''\n  final: direct\n  default_exit: '-'\n")

func TestStartRevisionUsesPrivateSnapshotUntilHelperExits(t *testing.T) {
	controller := testController(t, "normal")
	original := controller.options.ConfigPath
	if err := os.WriteFile(original, connectionSnapshotConfig, 0o600); err != nil {
		t.Fatal(err)
	}
	_, revision, err := ReadConfigDocument(original)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.StartRevision(revision); err != nil {
		t.Fatal(err)
	}
	waitState(t, controller, "running")
	controller.mu.Lock()
	args := append([]string(nil), controller.command.Args...)
	controller.mu.Unlock()
	index := slices.Index(args, "-c")
	if index < 0 || index+1 >= len(args) {
		t.Fatal("helper has no configuration argument", args)
	}
	snapshot := args[index+1]
	if snapshot == original || filepath.Dir(snapshot) != filepath.Dir(original) {
		t.Fatal("helper did not receive a sibling configuration snapshot")
	}
	if err := privatefs.CheckFile(snapshot); err != nil {
		t.Fatal("snapshot is not private", err)
	}
	changed := append(append([]byte(nil), connectionSnapshotConfig...), "# later external edit\n"...)
	if err := os.WriteFile(original, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(snapshot)
	if err != nil || !bytes.Equal(data, connectionSnapshotConfig) {
		t.Fatal("running helper's configuration changed", err)
	}
	if err := controller.Stop(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := os.Stat(snapshot)
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("connection snapshot survived helper cleanup", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	data, err = os.ReadFile(original)
	if err != nil || !bytes.Equal(data, changed) {
		t.Fatal("cleanup overwrote the saved document", err)
	}
}

func TestStartRevisionRejectsStaleIntentAndCleansFailedLaunch(t *testing.T) {
	controller := testController(t, "normal")
	path := controller.options.ConfigPath
	if err := os.WriteFile(path, connectionSnapshotConfig, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, stale := range []string{"", "previous revision"} {
		if err := controller.StartRevision(stale); !errors.Is(err, ErrConfigChanged) {
			t.Fatal("stale click accepted", err)
		}
		if controller.Snapshot().Active {
			t.Fatal("stale click launched a helper")
		}
	}
	_, revision, err := ReadConfigDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	controller.options.Helper = filepath.Join(filepath.Dir(path), "missing-helper")
	if err := controller.StartRevision(revision); err == nil {
		t.Fatal("missing helper accepted")
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".tailsplit-config-*"))
	if err != nil || len(files) != 0 {
		t.Fatal("failed launch leaked a private snapshot", files, err)
	}
}
