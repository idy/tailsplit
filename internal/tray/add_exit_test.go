package tray

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/privatefs"
)

func TestStagedExitLoginProcess(t *testing.T) {
	mode := os.Getenv("TAILSPLIT_ADD_TEST_MODE")
	if mode == "" {
		return
	}
	path := ""
	for index, arg := range os.Args {
		if arg == "-c" && index+1 < len(os.Args) {
			path = os.Args[index+1]
		}
	}
	if !strings.Contains(strings.Join(os.Args, " "), "app-login-new") {
		os.Exit(2)
	}
	if err := privatefs.CheckFile(path); err != nil {
		os.Exit(2)
	}
	config, err := tailsplit.LoadDraft(path)
	if err != nil || len(config.Exits) != 1 || config.Exits[0].ID != "new-exit" || !config.Exits[0].AutoName {
		os.Exit(2)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if runtime.GOOS == "windows" {
		go func() { var b [1]byte; _, _ = io.ReadFull(os.Stdin, b[:]); cancel() }()
	}
	fmt.Println(`{"event":"status","exit":"new-exit","backend_state":"NeedsLogin","mode":"login"}`)
	if err := os.WriteFile(os.Getenv("TAILSPLIT_ADD_TEST_READY"), nil, 0o600); err != nil {
		os.Exit(3)
	}
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			os.Exit(0)
		case <-deadline.C:
			os.Exit(4)
		case <-ticker.C:
			if _, err := os.Stat(os.Getenv("TAILSPLIT_ADD_TEST_RELEASE")); err != nil {
				continue
			}
			if mode == "failure" {
				fmt.Println(`{"event":"error","message":"fixture authentication failed"}`)
				os.Exit(1)
			}
			if mode == "success" {
				fmt.Println(`{"event":"status","exit":"new-exit","backend_state":"Running","network_name":"fixture.ts.net","mode":"login"}`)
				fmt.Println(`{"event":"authenticated","mode":"login"}`)
			}
			os.Exit(0)
		}
	}
}

func TestAddExitPublishesOnlyAfterSuccessfulAuthentication(t *testing.T) {
	for _, outcome := range []string{"success", "failure", "no-auth", "cancel", "revision-conflict"} {
		t.Run(outcome, func(t *testing.T) {
			c := testController(t, "manual-events")
			path := c.options.ConfigPath
			original := []byte("# preserve this document\nversion: 1\nstate_dir: ./state\ntailscale: null\nexits: []\nrules: {source: '', default_exit: '-', global_exit: '-'}\n")
			if err := os.WriteFile(path, original, 0o600); err != nil {
				t.Fatal(err)
			}
			ready, release := filepath.Join(filepath.Dir(path), "ready"), filepath.Join(filepath.Dir(path), "release")
			mode := outcome
			if mode == "cancel" || mode == "revision-conflict" {
				mode = "success"
			}
			c.options.HelperArgs = []string{"-test.run=^TestStagedExitLoginProcess$", "--"}
			c.options.ExtraEnv = []string{"TAILSPLIT_ADD_TEST_MODE=" + mode, "TAILSPLIT_ADD_TEST_READY=" + ready, "TAILSPLIT_ADD_TEST_RELEASE=" + release}
			if err := c.AddExit("new-exit"); err != nil {
				t.Fatal(err)
			}
			c.mu.Lock()
			pendingPath, done := c.pendingAdd.path, c.done
			c.mu.Unlock()
			if err := privatefs.CheckFile(pendingPath); err != nil {
				t.Fatal("staged login config is not private:", err)
			}
			if !strings.HasPrefix(filepath.Base(pendingPath), ".tailsplit-config-") {
				t.Fatal("pending login did not retain the validated configuration candidate")
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, err := os.Stat(ready); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("login helper did not start")
				}
				time.Sleep(10 * time.Millisecond)
			}
			before, err := os.ReadFile(path)
			if err != nil || string(before) != string(original) {
				t.Fatal("pending authorization changed the saved exit list", err)
			}
			expected := original
			if outcome == "revision-conflict" {
				expected = append(append([]byte{}, original...), []byte("log_level: debug\n")...)
				if err := os.WriteFile(path, expected, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if outcome == "cancel" {
				if err := c.Stop(); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(release, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("login cleanup did not finish")
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if outcome == "success" {
				config, err := tailsplit.Load(path)
				if err != nil || len(config.Exits) != 1 || config.Exits[0].ID != "new-exit" {
					t.Fatal("authenticated exit was not committed", err)
				}
				if c.Snapshot().State != "authenticated" || !strings.Contains(string(after), "# preserve this document") {
					t.Fatal("success state or comments lost")
				}
			} else if string(after) != string(expected) {
				t.Fatal("failed/canceled/stale addition modified saved configuration")
			}
			if _, err := os.Stat(pendingPath); !os.IsNotExist(err) {
				t.Fatal("staged configuration was not removed", err)
			}
			if c.pendingAdd != nil {
				t.Fatal("pending operation survived cleanup")
			}
		})
	}
}

func TestAddExitRejectsStaleRevisionBeforeStagingOrStarting(t *testing.T) {
	c := testController(t, "manual-events")
	original := []byte("state_dir: ./state\ntailscale: null\nexits: []\nrules: {source: '', default_exit: '-', global_exit: '-'}\n")
	if err := os.WriteFile(c.options.ConfigPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := c.AddExit("new-exit", "stale-revision"); !errors.Is(err, ErrConfigChanged) {
		t.Fatal("stale Add reached helper startup", err)
	}
	if c.pendingAdd != nil || c.command != nil {
		t.Fatal("stale Add started a pending authentication operation")
	}
	staged, err := filepath.Glob(filepath.Join(filepath.Dir(c.options.ConfigPath), ".tailsplit-*"))
	if err != nil || len(staged) != 0 {
		t.Fatal("stale Add left staged configuration", staged, err)
	}
}

func TestAddExitHelperStartFailureRemovesRetainedCandidate(t *testing.T) {
	c := testController(t, "manual-events")
	original := []byte("state_dir: ./state\ntailscale: null\nexits: []\nrules: {source: '', default_exit: '-', global_exit: '-'}\n")
	if err := os.WriteFile(c.options.ConfigPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	c.options.Helper = filepath.Join(filepath.Dir(c.options.ConfigPath), "missing-helper")
	if err := c.AddExit("new-exit"); err == nil {
		t.Fatal("missing helper unexpectedly started")
	}
	if c.pendingAdd != nil || c.command != nil {
		t.Fatal("failed helper startup retained a pending authentication")
	}
	staged, err := filepath.Glob(filepath.Join(filepath.Dir(c.options.ConfigPath), ".tailsplit-*"))
	if err != nil || len(staged) != 0 {
		t.Fatal("failed helper startup retained a configuration candidate", staged, err)
	}
	if saved, err := os.ReadFile(c.options.ConfigPath); err != nil || string(saved) != string(original) {
		t.Fatal("failed startup changed saved configuration", err)
	}
}
