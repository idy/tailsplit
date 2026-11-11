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
	if snapshot := waitState(t, controller, "running"); snapshot.ActiveRevision != revision {
		t.Fatal("revision-checked helper lost its source revision", snapshot)
	}
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
	data, snapshotRevision, err := ReadConfigDocument(snapshot)
	if err != nil || !bytes.Equal(data, connectionSnapshotConfig) || snapshotRevision != revision || controller.Snapshot().ActiveRevision != snapshotRevision {
		t.Fatal("running helper's configuration or source revision changed", err)
	}
	if err := controller.Stop(); err != nil {
		t.Fatal(err)
	}
	if stopped := controller.Snapshot(); stopped.Active || stopped.ActiveRevision != "" {
		t.Fatal("stopped helper retained an active revision", stopped)
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
	_, changedRevision, err := ReadConfigDocument(original)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.StartRevision(changedRevision); err != nil {
		t.Fatal(err)
	}
	if snapshot := waitState(t, controller, "running"); snapshot.ActiveRevision != changedRevision || snapshot.ActiveRevision == revision {
		t.Fatal("revision-checked restart retained the previous source revision", snapshot)
	}
}

func TestTrafficStartsKeepExactSourceRevisionAcrossExternalEdits(t *testing.T) {
	controller := testController(t, "normal")
	path := controller.options.ConfigPath
	original, revision, err := ReadConfigDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot := controller.Snapshot(); snapshot.ActiveRevision != "" {
		t.Fatal("idle controller has an active revision", snapshot)
	}
	if err := controller.Start(); err != nil {
		t.Fatal(err)
	}
	if snapshot := waitState(t, controller, "running"); snapshot.ActiveRevision != revision {
		t.Fatal("ordinary traffic start lost its source revision", snapshot)
	}
	controller.mu.Lock()
	args := append([]string(nil), controller.command.Args...)
	controller.mu.Unlock()
	index := slices.Index(args, "-c")
	if index < 0 || index+1 >= len(args) || args[index+1] == path {
		t.Fatal("ordinary traffic start passed mutable saved configuration", args)
	}
	changed := append(append([]byte(nil), original...), "# external edit during traffic\n"...)
	if err := os.WriteFile(path, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	_, changedRevision, err := ReadConfigDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	data, helperRevision, err := ReadConfigDocument(args[index+1])
	if err != nil || !bytes.Equal(data, original) || helperRevision != revision || helperRevision == changedRevision || controller.Snapshot().ActiveRevision != helperRevision {
		t.Fatal("external edit changed the helper document or its reported revision", err)
	}
	if err := controller.Restart(); err != nil {
		t.Fatal(err)
	}
	if snapshot := waitState(t, controller, "running"); snapshot.ActiveRevision != changedRevision {
		t.Fatal("restart did not adopt the changed document revision", snapshot)
	}
	if err := controller.Stop(); err != nil {
		t.Fatal(err)
	}
	if stopped := controller.Snapshot(); stopped.Active || stopped.ActiveRevision != "" {
		t.Fatal("stop retained an active revision", stopped)
	}
	// Authentication and discovery can still own a helper, but have no active
	// traffic policy, even after a previous traffic session used this controller.
	for _, start := range []func() error{controller.StartLogin, controller.RefreshExitNodes} {
		if err := start(); err != nil {
			t.Fatal(err)
		}
		if snapshot := controller.Snapshot(); !snapshot.Active || snapshot.ActiveRevision != "" {
			t.Fatal("non-traffic helper exposed an active policy", snapshot)
		}
		if err := controller.Stop(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTrafficRevisionUsesCapturedDocumentWhenSavedFileChangesBeforeLaunch(t *testing.T) {
	controller := testController(t, "normal")
	path := controller.options.ConfigPath
	data, revision, err := ReadConfigDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	// Model an external edit between reading the launch document and starting
	// the process. Hashing the saved path at dispatch would report the wrong one.
	if err := os.WriteFile(path, append(append([]byte(nil), data...), "# changed before helper dispatch\n"...), 0o600); err != nil {
		t.Fatal(err)
	}
	controller.mu.Lock()
	err = controller.startConnectionLocked("proxy", "", data, "")
	controller.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot := waitState(t, controller, "running"); snapshot.ActiveRevision != revision {
		t.Fatal("active revision came from a later saved-file read", snapshot)
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
		if snapshot := controller.Snapshot(); snapshot.Active || snapshot.ActiveRevision != "" {
			t.Fatal("stale click launched a helper or reported an active revision", snapshot)
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
	files, err := filepath.Glob(filepath.Join(filepath.Dir(path), connectionSnapshotPrefix+"*"))
	if err != nil || len(files) != 0 {
		t.Fatal("failed launch leaked a private snapshot", files, err)
	}
}
