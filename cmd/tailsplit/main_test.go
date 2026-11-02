package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestInitDoesNotOverwriteAndUsesPrivatePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	var out, diagnostic bytes.Buffer
	if err := execute([]string{"init", "-c", path}, &out, &diagnostic); err != nil {
		t.Fatal(err)
	}
	stat, _ := os.Stat(path)
	if stat.Mode().Perm() != 0o600 {
		t.Fatal("init created a public config")
	}
	if err := execute([]string{"init", "-c", path}, &out, &diagnostic); err == nil {
		t.Fatal("init overwrote the existing config")
	}
}

func TestRenderAndStrictCommandArguments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("version: 1\nstate_dir: ./state\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, diagnostic bytes.Buffer
	if err := execute([]string{"render", "-c", path}, &out, &diagnostic); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"mixed"`) {
		t.Fatal("render did not emit native sing-box config")
	}
	for _, args := range [][]string{
		{"unknown"}, {"rules"}, {"version", "extra"},
		{"render", "-c", path, "ignored"}, {"render", "--events", "-c", path},
	} {
		if err := execute(args, &out, &diagnostic); err == nil {
			t.Fatalf("ignored invalid arguments: %v", args)
		}
	}
}

func TestParentExitCancelsBundledHelper(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var parent atomic.Int32
	parent.Store(100)
	done := make(chan struct{})
	go func() {
		watchParent(ctx, 100, func() int { return int(parent.Load()) }, cancel)
		close(done)
	}()
	parent.Store(1)
	select {
	case <-done:
		if ctx.Err() == nil {
			t.Fatal("helper survived its owning app")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("parent-exit monitor did not stop")
	}
}
