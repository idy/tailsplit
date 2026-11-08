package appservice

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/idy/tailsplit"
)

func TestFocusedFirstMigrationImportsEverySavedExitOnce(t *testing.T) {
	for _, focus := range []string{tailsplit.MainExit, "office"} {
		t.Run(focus, func(t *testing.T) {
			config, err := tailsplit.Normalize(tailsplit.Config{
				StateDir: t.TempDir(),
				Tailscale: &tailsplit.TailscaleConfig{
					Hostname: "fixture-main", ExitNode: "100.64.0.10",
				},
				Exits: []tailsplit.ExitConfig{
					{ID: "office", Hostname: "fixture-office", ExitNode: "100.101.0.11"},
					{ID: "spare", Hostname: "fixture-spare"},
				},
			}, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			for _, id := range config.ExitIDs() {
				directory := config.ExitStateDir(id)
				if err := os.MkdirAll(directory, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(directory, "tailscaled.state"), []byte("fixture-state-"+id), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			focused, err := config.FocusExit(focus)
			if err != nil {
				t.Fatal(err)
			}
			files, err := userFiles(&focused, true)
			if err != nil {
				t.Fatal(err)
			}
			if string(files.TailscaleState) != "fixture-state-main" || len(files.ExitStates) != len(config.Exits) {
				t.Fatalf("focused request omitted saved identities: %+v", files)
			}
			// Match the ordinary-user request, then the service's independent
			// normalization and one-time import, without opening any endpoint.
			focused.StateDir = ""
			data, err := json.Marshal(Request{Protocol: Protocol, Op: "login", Config: &focused, Files: files})
			if err != nil {
				t.Fatal(err)
			}
			request, err := readRequest(bufio.NewReader(bytes.NewReader(append(data, '\n'))))
			if err != nil {
				t.Fatal(err)
			}
			base := privateTestDirectory(t)
			directory := runtimeUserDirectory(base, "fixture-user")
			received, err := serviceConfig(*request.Config, directory)
			if err != nil {
				t.Fatal(err)
			}
			if received.Focus != focus || !reflect.DeepEqual(received.Exits, config.Exits) {
				t.Fatal("service discarded focus or the complete exit inventory")
			}
			if _, err := prepareRuntimeUser(base, "fixture-user", request.Files); err != nil {
				t.Fatal(err)
			}
			if !runtimeInitialized(directory) {
				t.Fatal("first migration did not finish")
			}
			for _, id := range config.ExitIDs() {
				path := filepath.Join(received.ExitStateDir(id), "tailscaled.state")
				state, err := os.ReadFile(path)
				if err != nil || string(state) != "fixture-state-"+id {
					t.Fatalf("saved identity %q was not imported to its own directory: %q, %v", id, state, err)
				}
				if err := os.WriteFile(path, []byte("logged-out-"+id), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			// A subsequent stale import must never resurrect any logged-out
			// identity, including the exits outside the original runtime focus.
			if _, err := prepareRuntimeUser(base, "fixture-user", request.Files); err != nil {
				t.Fatal(err)
			}
			for _, id := range config.ExitIDs() {
				state, err := os.ReadFile(filepath.Join(received.ExitStateDir(id), "tailscaled.state"))
				if err != nil || string(state) != "logged-out-"+id {
					t.Fatalf("stale migration restored identity %q: %q, %v", id, state, err)
				}
			}
		})
	}
}
