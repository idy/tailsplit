package configweb

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/tray"
)

func TestStateExposesSeparatePrimaryAndAdditionalDNSSuffixes(t *testing.T) {
	server, path, _ := testServer(t)
	server.options.Snapshot = func() tray.Snapshot {
		return tray.Snapshot{
			BackendState: "Running", NetworkName: "example.com", MagicDNSSuffix: "tail123.ts.net",
			LoginURL: "https://login.tailscale.com/a/private-auth-token",
			Exits: map[string]tray.ExitSnapshot{
				"office": {BackendState: "Running", NetworkName: "office.example", MagicDNSSuffix: "tail456.ts.net"},
				"legacy": {BackendState: "Running", NetworkName: "legacy.example"},
			},
		}
	}
	status, body := request(t, server, http.MethodGet, "/api/state", nil, nil)
	var state struct {
		Network string `json:"network"`
		Suffix  string `json:"magic_dns_suffix"`
		Exits   map[string]struct {
			Network string `json:"network"`
			Suffix  string `json:"magic_dns_suffix"`
		} `json:"exits"`
	}
	if err := json.Unmarshal(body, &state); err != nil || status != http.StatusOK {
		t.Fatalf("state: %d %s, %v", status, body, err)
	}
	if state.Network != "example.com" || state.Suffix != "tail123.ts.net" ||
		state.Exits["office"].Network != "office.example" || state.Exits["office"].Suffix != "tail456.ts.net" || state.Exits["legacy"].Suffix != "" {
		t.Fatalf("safe API conflated display names and DNS suffixes: %s", body)
	}
	for _, private := range []string{path, server.token, "private-auth-token", "login_url", "auth_url", "LoginURL"} {
		if bytes.Contains(body, []byte(private)) {
			t.Fatalf("safe state API leaked %q", private)
		}
	}
}

func TestExitSelectionPersistsSeparateDNSSuffixThroughStructuredEdits(t *testing.T) {
	server, path, applied := testServer(t)
	original, revision, err := tray.ReadConfigDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	exit := map[string]any{"id": "office", "name": "gateway@example.com", "auto_name": true,
		"exit_node": "100.101.0.11", "tailnet": "example.com", "magic_dns_suffix": "tail123.ts.net"}
	status, body := request(t, server, http.MethodPost, "/api/config", map[string]any{
		"yaml": string(original), "revision": revision,
		"changes": map[string]any{"exits": []map[string]any{exit}, "rules.default_exit": "-", "rules.global_exit": "-"},
	}, nil)
	if status != http.StatusOK {
		t.Fatalf("known-fields structured save: %d %s", status, body)
	}
	config, err := tailsplit.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Exits) != 1 || config.Exits[0].MagicDNSSuffix != "tail123.ts.net" || config.Exits[0].Tailnet != "example.com" ||
		config.Exits[0].Name != "gateway@example.com" || !config.Exits[0].AutoName {
		t.Fatalf("structured save conflated exit identity fields: %+v", config.Exits)
	}
	saved, revision, err := tray.ReadConfigDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	exit["magic_dns_suffix"] = "invalid domain.ts.net"
	status, body = request(t, server, http.MethodPost, "/api/config", map[string]any{
		"yaml": string(saved), "revision": revision, "changes": map[string]any{"exits": []map[string]any{exit}},
	}, nil)
	if status != http.StatusUnprocessableEntity || applied.Load() != 1 {
		t.Fatalf("invalid suffix reached apply: %d %s, applies=%d", status, body, applied.Load())
	}
	after, afterRevision, err := tray.ReadConfigDocument(path)
	if err != nil || afterRevision != revision || !bytes.Equal(after, saved) {
		t.Fatalf("invalid suffix replaced saved configuration: %v", err)
	}
}
