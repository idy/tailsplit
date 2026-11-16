//go:build !windows

package appservice

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/idy/tailsplit"
)

func TestInstallationReadinessRequiresAuthenticatedMatchingHello(t *testing.T) {
	for _, scenario := range []string{"ready", "missing", "no-hello", "wrong-event", "wrong-protocol", "wrong-build", "wrong-user"} {
		t.Run(scenario, func(t *testing.T) {
			executable := filepath.Join(t.TempDir(), "helper")
			if err := os.WriteFile(executable, []byte("installed helper fixture"), 0o755); err != nil {
				t.Fatal(err)
			}
			buildID, err := executableID(executable)
			if err != nil {
				t.Fatal(err)
			}
			directory, err := os.MkdirTemp("/tmp", "tailsplit-ready-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(directory) })
			socket := filepath.Join(directory, "control")
			expectedUID := strconv.Itoa(os.Getuid())
			if scenario == "wrong-user" {
				expectedUID = strconv.Itoa(os.Getuid() + 1)
			}
			if scenario != "missing" {
				listener, err := net.Listen("unix", socket)
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
				done := make(chan struct{})
				go func() {
					defer close(done)
					connection, err := listener.Accept()
					if err != nil {
						return
					}
					defer connection.Close()
					if scenario == "no-hello" {
						return
					}
					var request Request
					if err := json.NewDecoder(connection).Decode(&request); err != nil {
						return // The wrong peer must be rejected before hello.
					}
					if request.Op != "hello" || request.Protocol != Protocol || request.Config != nil {
						t.Error("readiness attempted an operation other than protocol hello")
					}
					info := Info{Event: "service", Protocol: Protocol, BuildID: buildID}
					switch scenario {
					case "wrong-event":
						info.Event = "error"
					case "wrong-protocol":
						info.Protocol++
					case "wrong-build":
						info.BuildID = "previous-helper"
					}
					_ = json.NewEncoder(connection).Encode(info)
				}()
				defer func() { _ = listener.Close(); <-done }()
			}
			err = checkInstalled(context.Background(), executable, socket, expectedUID)
			if (err == nil) != (scenario == "ready") {
				t.Fatalf("unexpected readiness result for %s: %v", scenario, err)
			}
		})
	}
}

func TestInstallationReadinessDoesNotStartAnEngine(t *testing.T) {
	path, _, cancel, done := testServer(t, os.Getuid(), func(context.Context, tailsplit.Config, io.Writer) error {
		t.Error("installation readiness started a network engine")
		return nil
	})
	// A different helper is still a failed installation, even though the
	// authenticated service can answer a valid hello without starting traffic.
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := checkInstalled(context.Background(), executable, path, strconv.Itoa(os.Getuid())); err == nil {
		t.Fatal("readiness accepted a service from another build")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
