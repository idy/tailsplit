package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/idy/tailsplit"
)

func TestTrafficStreamSendsInitialSessionSampleAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	want := tailsplit.TrafficSnapshot{SessionID: "sample-session", SampledAt: time.Now().UTC(),
		Routes: map[string]tailsplit.TrafficCounters{"direct": {UploadBytes: 13, DownloadBytes: 29}}}
	var output bytes.Buffer
	stream := &eventStream{encoder: json.NewEncoder(&output)}
	if err := streamTraffic(ctx, func() tailsplit.TrafficSnapshot {
		cancel()
		return want
	}, stream); err != nil {
		t.Fatal(err)
	}
	var event struct {
		Event   string                    `json:"event"`
		Traffic tailsplit.TrafficSnapshot `json:"traffic"`
	}
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	if event.Event != "traffic" || event.Traffic.SessionID != want.SessionID || event.Traffic.Routes["direct"] != want.Routes["direct"] {
		t.Fatal("helper did not emit the real engine sample:", event)
	}
}

type trafficFailedWriter struct{}

func (trafficFailedWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestTrafficStreamReturnsChannelFailure(t *testing.T) {
	err := streamTraffic(context.Background(), func() tailsplit.TrafficSnapshot { return tailsplit.TrafficSnapshot{} },
		&eventStream{encoder: json.NewEncoder(trafficFailedWriter{})})
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal("helper ignored unavailable telemetry channel:", err)
	}
}
