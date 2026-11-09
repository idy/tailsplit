package appservice

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/idy/tailsplit"
)

func TestImportQueryMetadataIsBoundedByTheCurrentInventory(t *testing.T) {
	ids := []string{tailsplit.MainExit}
	for index := 1; index <= maxExits; index++ {
		ids = append(ids, fmt.Sprintf("exit-%d-%s", index, strings.Repeat("x", 25)))
	}
	if err := validateImportIDs(ids); err != nil {
		t.Fatal("maximum supported query was rejected:", err)
	}
	data, err := json.Marshal(importReply{Event: "imports", Protocol: Protocol, Plan: ImportPlan{IDs: ids, Cache: true}})
	if err != nil || len(data) >= 4096 {
		t.Fatal("bounded inventory exceeds the client status response limit", len(data), err)
	}
	for _, invalid := range [][]string{
		append(append([]string(nil), ids...), "extra-overflow"),
		append(append([]string(nil), ids[1:]...), "ninth-extra"),
		{"office", "office"}, {tailsplit.MainExit, tailsplit.MainExit},
		{"../office"}, {"con"}, {""},
	} {
		data, err := json.Marshal(Request{Protocol: Protocol, Op: "imports", ImportIDs: invalid})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := readRequest(bufio.NewReader(bytes.NewReader(append(data, '\n')))); err == nil {
			t.Fatalf("service accepted invalid import metadata %v", invalid)
		}
	}
}

func TestImportQueryRejectsUnsupportedOrUnrelatedRepliesAndReleasesLease(t *testing.T) {
	for name, reply := range map[string]any{
		"older-status-only-service": Info{Event: "service", Protocol: Protocol},
		"wrong-protocol":            importReply{Event: "imports", Protocol: Protocol + 1},
		"unrelated-identity":        importReply{Event: "imports", Protocol: Protocol, Plan: ImportPlan{IDs: []string{"office"}}},
		"duplicate-identity":        importReply{Event: "imports", Protocol: Protocol, Plan: ImportPlan{IDs: []string{tailsplit.MainExit, tailsplit.MainExit}}},
	} {
		t.Run(name, func(t *testing.T) {
			config := tailsplit.Config{StateDir: privateTestDirectory(t), Tailscale: &tailsplit.TailscaleConfig{}}
			dial := func(context.Context) (net.Conn, error) {
				client, server := net.Pipe()
				go func() {
					defer server.Close()
					var request Request
					if err := json.NewDecoder(server).Decode(&request); err != nil {
						return
					}
					_ = json.NewEncoder(server).Encode(reply)
				}()
				return client, nil
			}
			if connection, err := openLeasedSession(context.Background(), "login", config, "", Info{}, dial); err == nil {
				_ = connection.Close()
				t.Fatal("client accepted an unsupported import reply")
			}
			if release, err := tailsplit.LockSavedState(config.StateDir); err != nil {
				t.Fatal("failed handshake retained the legacy lease", err)
			} else {
				_ = release()
			}
		})
	}
}

func TestServiceStateIDsRejectWindowsDeviceBasenames(t *testing.T) {
	reserved := []string{"con", "prn", "aux", "nul"}
	for index := 1; index <= 9; index++ {
		reserved = append(reserved, fmt.Sprintf("com%d", index), fmt.Sprintf("lpt%d", index))
	}
	for _, id := range reserved {
		t.Run(id, func(t *testing.T) {
			if validExitID(id) {
				t.Fatalf("service state path accepts Windows device basename %q", id)
			}
			data, err := json.Marshal(Request{Protocol: Protocol, Op: "start", Files: Files{ExitStates: map[string][]byte{id: []byte("state")}}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := readRequest(bufio.NewReader(bytes.NewReader(append(data, '\n')))); err == nil {
				t.Fatalf("service accepted migration state keyed by %q", id)
			}
		})
	}
}

func TestServiceStateIDsKeepOrdinaryNames(t *testing.T) {
	for _, id := range []string{"office", "exit-1", "console", "con-office", "prn-1", "aux-gateway", "nul-2", "com0", "com10", "com1-office", "lpt0", "lpt10", "lpt9-office"} {
		t.Run(id, func(t *testing.T) {
			if !validExitID(id) {
				t.Fatalf("service rejects ordinary state directory ID %q", id)
			}
			data, err := json.Marshal(Request{Protocol: Protocol, Op: "start", Files: Files{ExitStates: map[string][]byte{id: []byte("state")}}})
			if err != nil {
				t.Fatal(err)
			}
			request, err := readRequest(bufio.NewReader(bytes.NewReader(append(data, '\n'))))
			if err != nil || string(request.Files.ExitStates[id]) != "state" {
				t.Fatalf("service changed valid migration state for %q: %+v, %v", id, request.Files, err)
			}
		})
	}
	for _, id := range []string{"main", "direct", "proxy", "CON", "Com1", " office ", "../office", "", "-office", "office-"} {
		if validExitID(id) {
			t.Fatalf("service accepted invalid state directory ID %q", id)
		}
	}
}
