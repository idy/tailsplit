package main

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/idy/tailsplit/internal/tray"
)

func TestDesktopPreviewBindsActiveRevisionUntilDisconnect(t *testing.T) {
	options := testDesktopPreview(t)
	previewAction(t, options, "login")
	original, revision, err := tray.ReadConfigDocument(options.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := options.Connect(context.Background(), revision); err != nil {
		t.Fatal(err)
	}
	if snapshot := options.Snapshot(); !snapshot.Active || snapshot.ActiveRevision != revision {
		t.Fatalf("preview connection did not record its revision: %+v", snapshot)
	}
	if err := os.WriteFile(options.ConfigPath, append(original, []byte("# external change\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	_, changed, err := tray.ReadConfigDocument(options.ConfigPath)
	if err != nil || changed == revision {
		t.Fatalf("external edit did not change revision: %q, %v", changed, err)
	}
	if snapshot := previewAction(t, options, "refresh-exits"); snapshot.ActiveRevision != revision {
		t.Fatal("a read-only refresh acknowledged unapplied configuration", snapshot)
	}
	if err := options.Connect(context.Background(), revision); !errors.Is(err, tray.ErrConfigChanged) {
		t.Fatal("stale preview connect was not rejected", err)
	}
	if snapshot := previewAction(t, options, "disconnect"); snapshot.ActiveRevision != "" {
		t.Fatal("disconnect retained the active revision", snapshot)
	}
	if err := options.Connect(context.Background(), changed); err != nil {
		t.Fatal(err)
	}
	if snapshot := options.Snapshot(); snapshot.ActiveRevision != changed {
		t.Fatal("reconnect retained an old revision", snapshot)
	}
	previewAction(t, options, "disconnect")
	if snapshot := previewAction(t, options, "login"); snapshot.ActiveRevision != "" {
		t.Fatal("device-only login invented a traffic revision", snapshot)
	}
}

func TestDesktopPreviewApplyAdvancesActiveRevision(t *testing.T) {
	options := testDesktopPreview(t)
	previewAction(t, options, "login")
	before := previewAction(t, options, "connect")
	if err := options.Apply(context.Background(), nil, func() error {
		data, revision, err := tray.ReadConfigDocument(options.ConfigPath)
		if err != nil {
			return err
		}
		updated, err := tray.PatchConfigDocument(data, tray.ConfigChange{Key: "log_level", Value: "debug"})
		if err != nil {
			return err
		}
		return tray.SaveConfigDocument(options.ConfigPath, revision, updated)
	}); err != nil {
		t.Fatal(err)
	}
	_, revision, err := tray.ReadConfigDocument(options.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot := options.Snapshot(); snapshot.ActiveRevision == "" || snapshot.ActiveRevision == before.ActiveRevision || snapshot.ActiveRevision != revision {
		t.Fatalf("applied preview retained the wrong running revision: %+v", snapshot)
	}
}
