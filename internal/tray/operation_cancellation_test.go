package tray

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"runtime"
	"slices"
	"sync"
	"syscall"
	"testing"
	"time"
)

// The helper's socket is a shutdown/logout barrier. Unlike sleeps, releasing it
// determines exactly when the subprocess can finish its admitted action.
func TestOperationCancellationHelper(t *testing.T) {
	address := os.Getenv("TAILSPLIT_TEST_CANCELLATION_BARRIER")
	if address == "" {
		return
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	connection, err := net.Dial("tcp", address)
	if err != nil {
		os.Exit(2)
	}
	defer connection.Close()
	if slices.Contains(os.Args, "logout") {
		fmt.Fprintln(connection, "logout")
		if os.Getenv("TAILSPLIT_TEST_BLOCK_LOGOUT") == "" {
			return
		}
	} else {
		// On Windows the controller stops a leased helper by closing stdin.
		if runtime.GOOS == "windows" {
			go func() { var b [1]byte; _, _ = io.ReadFull(os.Stdin, b[:]); cancel() }()
		}
		fmt.Fprintln(os.Stdout, `{"event":"status","backend_state":"Running","exit_online":true}`)
		fmt.Fprintln(connection, "running")
		<-ctx.Done()
		fmt.Fprintln(connection, "stopping")
	}
	var release [1]byte
	if _, err := io.ReadFull(connection, release[:]); err != nil {
		os.Exit(3)
	}
}

func cancellationBarrierController(t *testing.T, blockLogout bool) (*Controller, *net.TCPListener) {
	t.Helper()
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	c := testController(t, "normal")
	c.options.HelperArgs = []string{"-test.run=^TestOperationCancellationHelper$", "--"}
	c.options.ExtraEnv = append(c.options.ExtraEnv, "GORACE=atexit_sleep_ms=0", "TAILSPLIT_TEST_CANCELLATION_BARRIER="+listener.Addr().String())
	if blockLogout {
		c.options.ExtraEnv = append(c.options.ExtraEnv, "TAILSPLIT_TEST_BLOCK_LOGOUT=1")
	}
	return c, listener
}

func acceptCancellationBarrier(t *testing.T, listener *net.TCPListener, phase string) net.Conn {
	t.Helper()
	_ = listener.SetDeadline(time.Now().Add(5 * time.Second))
	connection, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	waitCancellationPhase(t, connection, phase)
	return connection
}

func waitCancellationPhase(t *testing.T, connection net.Conn, want string) {
	t.Helper()
	_ = connection.SetReadDeadline(time.Now().Add(5 * time.Second))
	var got string
	if _, err := fmt.Fscanln(connection, &got); err != nil || got != want {
		t.Fatalf("helper phase = %q, %v; want %q", got, err, want)
	}
}

func cancellationResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("operation did not finish")
		return nil
	}
}

func TestCancellationDuringHelperShutdownRejectsNextAction(t *testing.T) {
	for _, action := range []string{"update", "legacy-update", "logout", "legacy-logout"} {
		t.Run(action, func(t *testing.T) {
			c, listener := cancellationBarrierController(t, false)
			op := c.ForOperation("connection")
			if err := op.Start(); err != nil {
				t.Fatal(err)
			}
			helper := acceptCancellationBarrier(t, listener, "running")
			result := make(chan error, 1)
			updated := false
			go func() {
				update := func() error { updated = true; return nil }
				switch action {
				case "logout":
					result <- op.Logout()
				case "legacy-logout":
					result <- c.Logout()
				case "legacy-update":
					result <- c.Reconfigure(update)
				default:
					result <- c.ReconfigureOperation(op.ID, nil, update)
				}
			}()
			waitCancellationPhase(t, helper, "stopping")
			target := c.Snapshot().OperationID
			canceled, cancellation := make(chan struct{}), make(chan error, 1)
			go func() { cancellation <- c.CancelWithNotify("", target, func([]string) { close(canceled) }) }()
			select {
			case <-canceled:
			case <-time.After(5 * time.Second):
				t.Fatal("cancellation did not revoke admission")
			}
			if _, err := helper.Write([]byte{1}); err != nil {
				t.Fatal(err)
			}
			if err := cancellationResult(t, cancellation); err != nil {
				t.Fatal(err)
			}
			if err := cancellationResult(t, result); !errors.Is(err, ErrOperationCanceled) || updated {
				t.Fatal("cancellation admitted the next action", err, updated)
			}
			if err := c.ForOperation("new-owner").Start(); err != nil {
				t.Fatal(err)
			}
			newHelper := acceptCancellationBarrier(t, listener, "running")
			if err := c.CancelOperation(target); err != nil {
				t.Fatal(err)
			}
			if got := c.Snapshot(); !got.Active || got.OperationID != "new-owner" {
				t.Fatal("stale cancellation changed the newer owner", got)
			}
			// Cleanup may stop the newer helper only after its barrier is released.
			_, _ = newHelper.Write([]byte{1})
		})
	}
}

func TestCancellationJoinsAdmittedActionCompletion(t *testing.T) {
	for _, legacyStop := range []bool{false, true} {
		t.Run(map[bool]string{false: "guarded-cancel", true: "legacy-stop"}[legacyStop], func(t *testing.T) {
			for _, logout := range []bool{false, true} {
				t.Run(map[bool]string{false: "update", true: "logout"}[logout], func(t *testing.T) {
					c, listener := cancellationBarrierController(t, true)
					c.stopWait = 20 * time.Millisecond
					op := c.ForOperation("admitted")
					result := make(chan error, 1)
					var release func()
					if logout {
						go func() { result <- op.Logout() }()
						helper := acceptCancellationBarrier(t, listener, "logout")
						release = sync.OnceFunc(func() { _, _ = helper.Write([]byte{1}) })
					} else {
						updating, finish := make(chan struct{}), make(chan struct{})
						release = sync.OnceFunc(func() { close(finish) })
						go func() {
							result <- c.ReconfigureOperation(op.ID, nil, func() error {
								// Real update callbacks reenter controller read APIs.
								_ = c.Snapshot()
								close(updating)
								<-finish
								return nil
							})
						}()
						select {
						case <-updating:
						case <-time.After(5 * time.Second):
							release()
							t.Fatal("update was not admitted")
						}
					}
					defer release()
					if err := c.ForOperation("replacement").Stop(); err == nil || c.Snapshot().OperationID != op.ID {
						t.Fatal("a phase transition replaced the admitted action's owner", err)
					}
					if err := c.CancelOperation("unrelated"); err != nil {
						t.Fatal("unrelated cancellation joined this action", err)
					}
					cancel := func() error { return c.CancelOperation(op.ID) }
					if legacyStop {
						cancel = c.Stop
					}
					for range 2 {
						if err := cancel(); err == nil {
							t.Fatal("cancellation completed while admitted action was blocked")
						}
					}
					if err := c.ForOperation("premature-owner").Start(); err == nil {
						t.Fatal("cancellation reopened admission before action completion")
					}
					release()
					if err := cancellationResult(t, result); !errors.Is(err, ErrOperationCanceled) {
						t.Fatal("admitted action did not report cancellation after completing", err)
					}
					if err := cancel(); err != nil {
						t.Fatal("finished action stranded cancellation", err)
					}
				})
			}
		})
	}
}
