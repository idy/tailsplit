//go:build windows

package appservice

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/idy/tailsplit"
)

func TestWindowsPingCancellationHoldsStateLeaseUntilCleanup(t *testing.T) {
	client, service := net.Pipe()
	defer service.Close()
	released := make(chan struct{})
	connection := &stateLeaseConnection{Conn: client, release: func() error { close(released); return nil }}
	defer connection.Close()
	allowCleanup := make(chan struct{})
	finish := sync.OnceFunc(func() { close(allowCleanup) })
	defer finish()
	canceled := make(chan struct{})
	go func() {
		defer service.Close() // Match the service's EOF after the cleanup reply.
		var command [5]byte
		if _, err := io.ReadFull(service, command[:]); err != nil || string(command[:]) != "stop\n" {
			return
		}
		close(canceled)
		<-allowCleanup
		_ = json.NewEncoder(service).Encode(tailsplit.ExitPingReply{Error: "ping_canceled"})
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := readPingReply(ctx, connection)
		_ = connection.Close()
		done <- err
	}()
	cancel()
	select {
	case <-canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("the service did not receive cancellation")
	}
	select {
	case <-released:
		t.Fatal("the saved-state lease was released before cleanup")
	default:
	}
	finish()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation was lost: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the client did not finish after cleanup")
	}
	select {
	case <-released:
	default:
		t.Fatal("the saved-state lease was not released after cleanup")
	}
}
