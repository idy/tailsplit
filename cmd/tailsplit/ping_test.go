package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/idy/tailsplit"
)

func TestPingCommandReturnsBoundedCapabilityAndValidationFailures(t *testing.T) {
	for _, test := range []struct{ id, code string }{{"", "ping_target"}, {"100.64.0.10", "ping_unavailable"}} {
		var output bytes.Buffer
		if err := runPingCommand(context.Background(), false, tailsplit.Config{}, test.id, &output); err != nil {
			t.Fatal(err)
		}
		var reply tailsplit.ExitPingReply
		if json.Unmarshal(output.Bytes(), &reply) != nil || reply.Error != test.code || reply.Result != nil {
			t.Fatalf("unexpected private reply: %s", output.Bytes())
		}
	}
}
