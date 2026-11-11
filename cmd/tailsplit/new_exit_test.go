package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idy/tailsplit"
	singlog "github.com/sagernet/sing-box/log"
)

func TestAppLoginNewRequiresExplicitConfiguredAdditionalExitBeforeService(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := []byte("state_dir: ./untouched\ntailscale: {hostname: fixture-main}\nexits: [{id: new-exit}]\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, focus := range []string{"", "main", "missing", "../escape", "con"} {
		args := []string{"app-login-new", "--events", "-c", path}
		if focus != "" {
			args = append(args, "--exit", focus)
		}
		var out, diagnostic bytes.Buffer
		err := execute(args, &out, &diagnostic)
		if err == nil || !(strings.Contains(err.Error(), "explicit additional --exit") || strings.Contains(err.Error(), "selected exit is not configured")) {
			t.Errorf("invalid new-device scope reached service handling: %q %v", focus, err)
		}
		if out.Len() != 0 {
			t.Error("invalid focus emitted service/authentication output", out.String())
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "untouched")); !os.IsNotExist(err) {
		t.Fatal("invalid fresh login touched state", err)
	}
	if saved, err := os.ReadFile(path); err != nil || !bytes.Equal(saved, original) {
		t.Fatal("invalid fresh login changed saved config", err)
	}
}

func TestNewLoginEventsUseFreshAuthenticationAndOrdinaryLoginMode(t *testing.T) {
	var out bytes.Buffer
	config := tailsplit.Config{StateDir: filepath.Join(t.TempDir(), "untouched"), Focus: tailsplit.MainExit, Tailscale: &tailsplit.TailscaleConfig{}}
	// This scope is legal for ordinary saved login but forbidden for a fresh
	// addition. The fresh wrapper must reject it before constructing any engine.
	stream := newEventStream(t.Context(), &out, false)
	defer stream.close()
	err := runNewLoginEvents(t.Context(), config, stream)
	if err == nil || !strings.Contains(err.Error(), "configured additional exit") || strings.Contains(out.String(), `"event":"authenticated"`) {
		t.Fatal("new login used ordinary saved authentication", out.String(), err)
	}
	if _, err := os.Stat(config.StateDir); !os.IsNotExist(err) {
		t.Fatal("invalid fresh authentication touched state", err)
	}
	for _, fail := range []bool{false, true} {
		out.Reset()
		fixtureErr := errors.New("fixture authentication failed")
		config.Focus = "new-exit"
		config.Exits = []tailsplit.ExitConfig{{ID: "new-exit"}}
		config.TUN.Enabled = true
		stream := newEventStream(t.Context(), &out, true)
		defer stream.close()
		err := runLoginEventsWith(t.Context(), config, stream,
			func(_ context.Context, received tailsplit.Config, report func(tailsplit.Status), log singlog.PlatformWriter) error {
				if received.TUN.Enabled || received.Focus != "new-exit" || len(received.Exits) != 1 {
					t.Error("authentication changed its target or enabled traffic")
				}
				report(tailsplit.Status{Event: "status", Exit: "new-exit", BackendState: "Running"})
				log.WriteMessage(singlog.LevelInfo, "authentication fixture")
				if fail {
					return fixtureErr
				}
				return nil
			})
		if fail && !errors.Is(err, fixtureErr) || !fail && err != nil {
			t.Fatal("authentication result was lost", err)
		}
		decoder := json.NewDecoder(&out)
		var events []string
		authenticated := false
		for {
			var event map[string]any
			if err := decoder.Decode(&event); err == io.EOF {
				break
			} else if err != nil {
				t.Fatal(err)
			}
			events = append(events, event["event"].(string))
			if event["mode"] != nil && event["mode"] != "login" {
				t.Fatal("internal fresh-login operation leaked into public login mode", event)
			}
			if event["event"] == "authenticated" {
				authenticated = true
			}
		}
		if authenticated == fail {
			t.Fatal("terminal authentication did not match the completed operation", fail)
		}
		want := "preflight,status,log,authenticated"
		if fail {
			want = "preflight,status,log,error"
		}
		if got := strings.Join(events, ","); got != want {
			t.Fatal("login events were dropped or reordered:", got)
		}
	}
}
