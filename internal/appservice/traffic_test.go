package appservice

import (
	"bytes"
	"context"
	"io"
	"net"
	"testing"
)

func TestForwardPreservesTrafficTelemetryFromPrivilegedEngine(t *testing.T) {
	broker, core := net.Pipe()
	defer broker.Close()
	defer core.Close()
	const events = "{\"event\":\"traffic\",\"traffic\":{\"session_id\":\"engine-session\",\"sampled_at\":\"2026-11-10T10:00:00Z\",\"routes\":{\"main\":{\"upload_bytes\":17,\"download_bytes\":41},\"direct\":{\"upload_bytes\":0,\"download_bytes\":0}}}}\n{\"event\":\"stopped\"}\n"
	done := make(chan error, 1)
	go func() {
		_, err := io.WriteString(core, events)
		_ = core.Close()
		done <- err
	}()
	var output bytes.Buffer
	if err := Forward(context.Background(), broker, &output); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if output.String() != events {
		t.Fatal("appservice changed or dropped the engine's telemetry:", output.String())
	}
}
