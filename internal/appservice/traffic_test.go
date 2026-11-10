package appservice

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/idy/tailsplit"
)

type blockedForwardWriter struct {
	writer  *io.PipeWriter
	entered chan struct{}
	exited  chan struct{}
	calls   atomic.Int32
}

func (w *blockedForwardWriter) Write(data []byte) (int, error) {
	first := w.calls.Add(1) == 1
	if first {
		close(w.entered)
	}
	n, err := w.writer.Write(data)
	if first {
		close(w.exited)
	}
	return n, err
}

func TestForwardBlockedConsumerCancelsWithoutReleasingLiveState(t *testing.T) {
	config := migrationInventoryConfig(t, 2, tailsplit.RoutingSplit, "exit-2")
	canceled, cleanup := make(chan struct{}), make(chan struct{})
	finish := sync.OnceFunc(func() { close(cleanup) })
	defer finish()
	const traffic = "{\"event\":\"traffic\",\"traffic\":{\"session_id\":\"blocked-session\"}}\n"
	path, _, stopServer, serverDone := testServer(t, os.Getuid(), func(ctx context.Context, _ tailsplit.Config, output io.Writer) error {
		if _, err := io.WriteString(output, traffic); err != nil {
			return err
		}
		<-ctx.Done()
		close(canceled)
		<-cleanup
		return nil
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	connection, err := openLeasedSession(ctx, "login", config, "", Info{}, migrationDial(path))
	if err != nil {
		t.Fatal(err)
	}
	reader, pipeWriter := io.Pipe()
	writer := &blockedForwardWriter{writer: pipeWriter, entered: make(chan struct{}), exited: make(chan struct{})}
	forwardReturned, finished := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		defer close(finished)
		err := Forward(ctx, connection, writer)
		close(forwardReturned)
		done <- errors.Join(err, connection.Close())
	}()
	t.Cleanup(func() {
		cancel()
		finish()
		_ = reader.Close()
		_ = pipeWriter.Close()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("broker did not finish fixture cleanup")
		}
	})
	select {
	case <-writer.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("broker did not reach blocked stdout")
	}
	cancel()
	select {
	case <-canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("blocked stdout prevented service cancellation")
	}
	select {
	case <-forwardReturned:
		t.Fatal("broker returned before service engine cleanup")
	case <-time.After(50 * time.Millisecond):
	}
	if release, err := tailsplit.LockSavedState(config.StateDir); err == nil {
		_ = release()
		t.Fatal("blocked stdout released a live saved-state lease")
	}
	finish()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal("cancellation became an output error", err)
		}
	case <-time.After(time.Second):
		t.Fatal("broker retained the lease after cleanup because stdout was blocked")
	}
	if release, err := tailsplit.LockSavedState(config.StateDir); err != nil {
		t.Fatal("completed cleanup retained the saved-state lease", err)
	} else {
		_ = release()
	}
	select {
	case <-writer.exited:
		t.Fatal("broker closed caller-owned stdout to interrupt its writer")
	default:
	}
	// The single in-flight write owns its bytes and can finish after Forward.
	stopRead := time.AfterFunc(time.Second, func() { _ = reader.CloseWithError(context.DeadlineExceeded) })
	defer stopRead.Stop()
	data := make([]byte, len(traffic))
	if _, err := io.ReadFull(reader, data); err != nil || string(data) != traffic {
		t.Fatalf("cancellation changed the pending event: %q %v", data, err)
	}
	select {
	case <-writer.exited:
	case <-time.After(time.Second):
		t.Fatal("pending output did not finish when its consumer resumed")
	}
	if calls := writer.calls.Load(); calls != 1 {
		t.Fatal("broker queued more output after cancellation", calls)
	}
	stopServer()
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestForwardBlockedShutdownOutputStillWaitsForServiceEOF(t *testing.T) {
	for _, timing := range []string{"before terminal write", "during terminal write"} {
		t.Run(timing, func(t *testing.T) {
			directory := privateTestDirectory(t)
			unlock, err := tailsplit.LockSavedState(directory)
			if err != nil {
				t.Fatal(err)
			}
			broker, core := net.Pipe()
			connection := &stateLeaseConnection{Conn: broker, release: unlock}
			reader, pipeWriter := io.Pipe()
			writer := &blockedForwardWriter{writer: pipeWriter, entered: make(chan struct{}), exited: make(chan struct{})}
			ctx, cancel := context.WithCancel(t.Context())
			forwardReturned, finished := make(chan struct{}), make(chan struct{})
			done := make(chan error, 1)
			go func() {
				defer close(finished)
				err := Forward(ctx, connection, writer)
				close(forwardReturned)
				done <- errors.Join(err, connection.Close())
			}()
			t.Cleanup(func() {
				cancel()
				_ = reader.Close()
				_ = pipeWriter.Close()
				_ = core.Close()
				select {
				case <-finished:
				case <-time.After(5 * time.Second):
					t.Error("broker did not finish fixture cleanup")
				}
			})
			readStop := func() {
				t.Helper()
				_ = core.SetReadDeadline(time.Now().Add(time.Second))
				var command [5]byte
				if _, err := io.ReadFull(core, command[:]); err != nil || string(command[:]) != "stop\n" {
					t.Fatalf("service did not receive stop: %q %v", command, err)
				}
			}
			if timing == "before terminal write" {
				cancel()
				readStop()
			}
			const terminal = "{\"event\":\"stopped\"}\n"
			_ = core.SetWriteDeadline(time.Now().Add(time.Second))
			if _, err := io.WriteString(core, terminal); err != nil {
				t.Fatal(err)
			}
			select {
			case <-writer.entered:
			case <-time.After(time.Second):
				t.Fatal("shutdown event was not offered to output")
			}
			if timing == "during terminal write" {
				cancel()
				readStop()
			}
			// The terminal write must time out so the broker can resume draining.
			_ = core.SetWriteDeadline(time.Now().Add(time.Second))
			if _, err := io.WriteString(core, "{\"event\":\"log\",\"message\":\"cleanup\"}\n"); err != nil {
				t.Fatal("blocked shutdown output prevented service drain", err)
			}
			select {
			case <-forwardReturned:
				t.Fatal("shutdown output timeout replaced service EOF")
			default:
			}
			if release, err := tailsplit.LockSavedState(directory); err == nil {
				_ = release()
				t.Fatal("shutdown output timeout released live state")
			}
			_ = core.Close()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("blocked terminal output retained completed service state")
			}
			if release, err := tailsplit.LockSavedState(directory); err != nil {
				t.Fatal("service EOF did not release state", err)
			} else {
				_ = release()
			}
			select {
			case <-writer.exited:
				t.Fatal("broker closed caller-owned output")
			default:
			}
			stopRead := time.AfterFunc(time.Second, func() { _ = reader.CloseWithError(context.DeadlineExceeded) })
			defer stopRead.Stop()
			data := make([]byte, len(terminal))
			if _, err := io.ReadFull(reader, data); err != nil || string(data) != terminal {
				t.Fatalf("pending shutdown event changed: %q %v", data, err)
			}
			select {
			case <-writer.exited:
			case <-time.After(time.Second):
				t.Fatal("terminal writer did not finish when its consumer resumed")
			}
			if calls := writer.calls.Load(); calls != 1 {
				t.Fatal("shutdown output started additional writes", calls)
			}
		})
	}
}

type shortForwardWriter struct{}

func (shortForwardWriter) Write(data []byte) (int, error) { return len(data) - 1, nil }

func TestForwardStopsAndDrainsAfterOutputFailure(t *testing.T) {
	writeErr := errors.New("fixture output failed")
	for _, test := range []struct {
		name     string
		writer   io.Writer
		want     error
		canceled bool
	}{
		{"write error", failingEventWriter{writeErr}, writeErr, false},
		{"short write", shortForwardWriter{}, io.ErrShortWrite, false},
		{"canceled write error", failingEventWriter{writeErr}, writeErr, true},
		{"canceled short write", shortForwardWriter{}, io.ErrShortWrite, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			broker, core := net.Pipe()
			defer broker.Close()
			defer core.Close()
			serviceDone := make(chan error, 1)
			go func() {
				defer core.Close()
				_, err := io.WriteString(core, "{\"event\":\"traffic\"}\n")
				if err == nil {
					_ = core.SetReadDeadline(time.Now().Add(5 * time.Second))
					var command [5]byte
					_, err = io.ReadFull(core, command[:])
					if err == nil && string(command[:]) != "stop\n" {
						err = errors.New("output failure did not stop service")
					}
				}
				if err == nil {
					_, err = io.WriteString(core, "{\"event\":\"stopped\"}\n")
				}
				serviceDone <- err
			}()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if test.canceled {
				cancel()
			}
			if err := Forward(ctx, broker, test.writer); !errors.Is(err, test.want) {
				t.Fatal("forwarding lost its output error", err)
			}
			if err := <-serviceDone; err != nil {
				t.Fatal("forwarding failed to drain service cleanup", err)
			}
		})
	}
}

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
