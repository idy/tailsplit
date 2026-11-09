package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/idy/tailsplit"
)

func TestAppRunReportsTheTrafficPolicyFailureBeforeLaunch(t *testing.T) {
	for _, test := range []struct {
		name, owner string
		change      func(*tailsplit.Config)
	}{
		{"missing final owner", "removed-final", func(config *tailsplit.Config) { config.Rules.Final = "removed-final" }},
		{"missing group owner", "removed-group", func(config *tailsplit.Config) {
			config.Rules.Routes = []tailsplit.RouteConfig{{Exit: "removed-group", Domains: []string{"private.example"}}}
		}},
		{"selected node missing", tailsplit.MainExit, func(config *tailsplit.Config) { config.Tailscale.ExitNode = "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := tailsplit.Config{Tailscale: &tailsplit.TailscaleConfig{ExitNode: "100.64.0.10"},
				Rules: tailsplit.RulesConfig{DefaultExit: tailsplit.MainExit, Final: "direct", ProxyDomains: []string{"proxy.example"}}}
			test.change(&config)
			var output bytes.Buffer
			err := runAppCommand(context.Background(), config, &output, func(context.Context, string, tailsplit.Config, io.Writer) error {
				t.Fatal("invalid traffic policy reached service launch")
				return nil
			})
			if err == nil {
				t.Fatal("fixture should reject connection")
			}
			var event map[string]string
			if decodeErr := json.Unmarshal(output.Bytes(), &event); decodeErr != nil {
				t.Fatal("missing machine-readable failure", decodeErr)
			}
			if event["event"] != "error" || event["message"] != err.Error() || !strings.Contains(event["message"], test.owner) {
				t.Fatal("error event hid the policy owner or actual repair action", event, err)
			}
		})
	}
}
