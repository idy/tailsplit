package appservice

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
)

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
