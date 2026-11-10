package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/idy/tailsplit"
)

func TestPreviewAddPublishesOnlyItsSimulatedAuthenticatedDevice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("tailscale: null\nexits: []\nrules: {source: '', default_exit: '-', global_exit: '-'}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	options := previewOptions(path, "127.0.0.1:0", true)
	if err := options.Action(context.Background(), "add-exit:preview-new"); err != nil {
		t.Fatal(err)
	}
	config, err := tailsplit.Load(path)
	if err != nil || len(config.Exits) != 1 || config.Exits[0].ID != "preview-new" {
		t.Fatal("simulated authenticated profile missing", err)
	}
	if options.Snapshot().Exits["preview-new"].BackendState != "Running" {
		t.Fatal("preview committed before simulated authentication")
	}
	if err := options.Action(context.Background(), "add-exit:preview-new"); err == nil {
		t.Fatal("duplicate preview exit was accepted")
	}
}
