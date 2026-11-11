package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/idy/tailsplit"
	singlog "github.com/sagernet/sing-box/log"
)

type trafficCancelWriter struct {
	bytes.Buffer
	cancel context.CancelFunc
}

func (w *trafficCancelWriter) Write(data []byte) (int, error) {
	n, err := w.Buffer.Write(data)
	w.cancel()
	return n, err
}

func TestTrafficStreamSendsInitialSessionSampleAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	want := tailsplit.TrafficSnapshot{SessionID: "sample-session", SampledAt: time.Now().UTC(),
		Routes: map[string]tailsplit.TrafficCounters{"direct": {UploadBytes: 13, DownloadBytes: 29}}}
	output := &trafficCancelWriter{cancel: cancel}
	stream := newEventStream(ctx, output, false)
	defer stream.close()
	if err := streamTraffic(ctx, func() tailsplit.TrafficSnapshot { return want }, stream); err != nil {
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

func TestTrafficStreamSendsPeriodicSamples(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	stream := newEventStream(ctx, writer, false)
	defer stream.close()
	done := make(chan error, 1)
	go func() {
		var count uint64
		done <- streamTraffic(ctx, func() tailsplit.TrafficSnapshot {
			count++
			return tailsplit.TrafficSnapshot{SessionID: "periodic-session", Routes: map[string]tailsplit.TrafficCounters{"direct": {UploadBytes: count}}}
		}, stream)
	}()
	// Unblock Decode even if a regression produces no samples.
	stopRead := context.AfterFunc(ctx, func() { _ = reader.CloseWithError(ctx.Err()) })
	defer stopRead()
	decoder := json.NewDecoder(reader)
	for count := uint64(1); count <= 2; count++ {
		var event struct {
			Event   string                    `json:"event"`
			Traffic tailsplit.TrafficSnapshot `json:"traffic"`
		}
		if err := decoder.Decode(&event); err != nil {
			t.Fatal(err)
		}
		if event.Event != "traffic" || event.Traffic.SessionID != "periodic-session" || event.Traffic.Routes["direct"].UploadBytes != count {
			t.Fatal("helper lost a periodic sample or reordered output:", event)
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal("cancellation became a telemetry error:", err)
		}
	case <-time.After(time.Second):
		t.Fatal("periodic traffic stream did not stop")
	}
}

type trafficFailedWriter struct{}

func (trafficFailedWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

type trafficShortWriter struct{}

func (trafficShortWriter) Write(data []byte) (int, error) { return len(data) - 1, nil }

func TestTrafficStreamReturnsChannelFailure(t *testing.T) {
	for _, test := range []struct {
		name   string
		writer io.Writer
		want   error
	}{
		{"writer error", trafficFailedWriter{}, io.ErrClosedPipe},
		{"short write", trafficShortWriter{}, io.ErrShortWrite},
	} {
		t.Run(test.name, func(t *testing.T) {
			stream := newEventStream(t.Context(), test.writer, true)
			defer stream.close()
			err := streamTraffic(t.Context(), func() tailsplit.TrafficSnapshot { return tailsplit.TrafficSnapshot{} }, stream)
			if !errors.Is(err, test.want) {
				t.Fatal("helper ignored unavailable telemetry channel:", err)
			}
			// Cleanup and ignored log/status errors must not retry a failed output.
			stream.WriteMessage(singlog.LevelInfo, "engine closing")
			stream.close()
			if err := stream.send(tailsplit.Status{Event: "status"}); !errors.Is(err, test.want) {
				t.Fatal("cleanup lost the actual writer error:", err)
			}
		})
	}
}

type trafficBlockedWriter struct {
	writer  *io.PipeWriter
	entered chan struct{}
	exited  chan struct{}
	calls   atomic.Int32
}

func (w *trafficBlockedWriter) Write(data []byte) (int, error) {
	if w.calls.Add(1) == 1 {
		close(w.entered)
	}
	n, err := w.writer.Write(data)
	if w.calls.Load() == 1 {
		close(w.exited)
	}
	return n, err
}

func TestTrafficStreamBlockedReaderAllowsCancellationAndCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	reader, pipeWriter := io.Pipe()
	defer reader.Close()
	defer pipeWriter.Close()
	writer := &trafficBlockedWriter{writer: pipeWriter, entered: make(chan struct{}), exited: make(chan struct{})}
	stream := newEventStream(ctx, writer, true)
	defer stream.close()
	done := make(chan error, 1)
	cleanup := make(chan error, 1)
	go func() {
		// Engine.Close can emit logs and status before releasing TUN/DNS/state.
		// Exercise those same synchronous callbacks from deferred cleanup.
		defer func() {
			stream.WriteMessage(singlog.LevelInfo, "restoring TUN and DNS")
			cleanup <- stream.send(tailsplit.Status{Event: "status", BackendState: "Stopped"})
		}()
		done <- streamTraffic(ctx, func() tailsplit.TrafficSnapshot {
			return tailsplit.TrafficSnapshot{SessionID: "blocked-session"}
		}, stream)
	}()
	select {
	case <-writer.entered:
	case <-time.After(time.Second):
		t.Fatal("traffic stream did not reach the blocked writer")
	}
	statusDone := make(chan error, 1)
	logDone := make(chan struct{})
	go func() { statusDone <- stream.send(tailsplit.Status{Event: "status", BackendState: "Running"}) }()
	go func() {
		stream.WriteMessage(singlog.LevelInfo, "pending engine log")
		close(logDone)
	}()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal("cancellation became a telemetry error:", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked output prevented traffic cancellation")
	}
	select {
	case err := <-cleanup:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("cleanup status did not stop on cancellation:", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked output prevented deferred cleanup")
	}
	select {
	case err := <-statusDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("pending status did not stop on cancellation:", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked output retained a status sender")
	}
	select {
	case <-logDone:
	case <-time.After(time.Second):
		t.Fatal("blocked output retained a log sender")
	}
	select {
	case <-writer.exited:
		t.Fatal("cancellation closed caller-owned output instead of releasing senders")
	default:
	}
	// Cancellation leaves the caller's pipe usable. Let the single in-flight
	// write finish, then ensure no queued logs/status/events are emitted.
	stopRead := time.AfterFunc(time.Second, func() { _ = reader.CloseWithError(context.DeadlineExceeded) })
	defer stopRead.Stop()
	var event struct {
		Event   string                    `json:"event"`
		Traffic tailsplit.TrafficSnapshot `json:"traffic"`
	}
	if err := json.NewDecoder(reader).Decode(&event); err != nil || event.Event != "traffic" || event.Traffic.SessionID != "blocked-session" {
		t.Fatal("cancellation damaged the caller's output:", event, err)
	}
	select {
	case <-stream.done:
	case <-time.After(time.Second):
		t.Fatal("event worker survived after its writer was released")
	}
	if calls := writer.calls.Load(); calls != 1 {
		t.Fatal("canceled stream continued writing queued events:", calls)
	}
}
