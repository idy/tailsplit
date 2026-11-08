package appservice

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
)

func TestRequestAccommodatesAllSupportedMigrationFiles(t *testing.T) {
	state := bytes.Repeat([]byte("x"), maxState)
	request := Request{Protocol: Protocol, Op: "start", Files: Files{
		TailscaleState: state, ExitStates: make(map[string][]byte),
		Cache: bytes.Repeat([]byte("c"), maxCache), Rules: bytes.Repeat([]byte("r"), maxRules),
	}}
	for index := 0; index < maxExits; index++ {
		request.Files.ExitStates[fmt.Sprintf("exit-%d", index)] = state
	}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) <= 20<<20 || len(data)+1 > maxRequest {
		t.Fatalf("aggregate migration budget is wrong: encoded=%d limit=%d", len(data), maxRequest)
	}
	decoded, err := readRequest(bufio.NewReader(bytes.NewReader(append(data, '\n'))))
	if err != nil {
		t.Fatal("individually valid migration files exceed request framing:", err)
	}
	if len(decoded.Files.ExitStates) != maxExits || len(decoded.Files.Cache) != maxCache || len(decoded.Files.Rules) != maxRules {
		t.Fatal("aggregate migration lost supported file contents")
	}
}

func TestServiceExitIDsRejectReservedRoutingNames(t *testing.T) {
	for _, id := range []string{"main", "direct", "proxy"} {
		if validExitID(id) {
			t.Fatalf("reserved exit ID accepted by service: %s", id)
		}
	}
}
