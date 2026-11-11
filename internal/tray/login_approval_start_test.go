package tray

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/idy/tailsplit"
)

func TestLoginBrowserApprovalAfterSelectionBeforeActualProcessStart(t *testing.T) {
	for _, device := range []string{tailsplit.MainExit, "office"} {
		t.Run(device, func(t *testing.T) {
			c := testController(t, "manual-events")
			c.options.ExtraEnv = append(c.options.ExtraEnv, "GORACE=atexit_sleep_ms=0")
			if err := c.ForOperation("helper-owner").StartExitLogin(device); err != nil {
				t.Fatal(err)
			}
			waitState(t, c, "connecting")
			const link = "https://login.tailscale.com/a/approval-start"
			exit := ""
			if device != tailsplit.MainExit {
				exit = `,"exit":"` + device + `"`
			}
			status := func(backend string) {
				c.onEvent([]byte(`{"event":"status","backend_state":"` + backend + `","auth_url":"` + link + `"` + exit + `}`))
			}
			status("NeedsLogin")
			var flow LoginFlow
			flow.RequestOperation("logical-intent", "helper-owner")
			request := flow.NextBrowserRequest(c.Snapshot())
			if request.URL != link || request.OperationID != "logical-intent" || request.OwnerID != "helper-owner" {
				t.Fatal("fixture did not select the device's browser request", request)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			command := browserProcessFixture(ctx, c)
			// A short-lived real process makes an erroneous admission finish without
			// opening a browser or waiting for the fixture's timeout.
			command.Args = append(command.Args, "discover")
			command.Env = append(os.Environ(), "TAILSPLIT_TEST_HELPER=discovery", "GORACE=atexit_sleep_ms=0")
			selected, release := make(chan struct{}), make(chan struct{})
			releaseStart := sync.OnceFunc(func() { close(release) })
			defer releaseStart()
			result := make(chan error, 1)
			go func() {
				result <- openLoginPage(ctx, request.URL, "", func(context.Context, string, ...string) error {
					return runBrowserCommand(ctx, command, func(start func() error) error {
						close(selected)
						<-release // Browser discovery finishes after the status change.
						return c.AuthorizeLoginBrowserStart(request, start)
					})
				})
			}()
			select {
			case <-selected:
			case <-ctx.Done():
				t.Fatal("launcher did not reach the guarded Start boundary")
			}
			status("NeedsMachineAuth")
			if snapshot := c.Snapshot(); snapshot.LoginURL != request.URL || snapshot.LoginExitID() != device {
				t.Fatal("fixture changed the browser URL or device", snapshot)
			}
			releaseStart()
			err := <-result
			if !errors.Is(err, ErrOperationCanceled) || errors.Is(err, ErrLoginURLChanged) || command.Process != nil {
				t.Fatal("approval transition admitted an actual browser process or became a URL refresh", err, command.Process)
			}
			flow.Complete(err)
			if flow.Opening() || flow.pending || flow.OperationCanceled(request.OperationID) || flow.OperationCanceled(request.OwnerID) {
				t.Fatal("approval rejection left an opening intent or recorded an operation tombstone")
			}
			for _, id := range []string{request.OperationID, request.OwnerID} {
				if err := c.ForOperation(id).Check(); err != nil {
					t.Fatal("approval rejection revoked a live operation", id, err)
				}
			}
			if snapshot := c.Snapshot(); !snapshot.Active || snapshot.OperationID != request.OwnerID || snapshot.LoginURL != link {
				t.Fatal("approval rejection disrupted the login helper", snapshot)
			}
			if !flow.RequestOperation(request.OperationID, request.OwnerID) {
				t.Fatal("approval rejection prevented a fresh explicit browser intent")
			}
			if flow.NextBrowserRequest(c.Snapshot()).URL != "" || !flow.pending || flow.Message() != "等待 Tailnet 管理员批准此设备" {
				t.Fatal("the live helper no longer waits for machine approval", flow.Message())
			}
		})
	}
}

func TestLoginBrowserStartChecksCurrentTrafficDeviceApproval(t *testing.T) {
	for _, mode := range []string{"proxy", "tun"} {
		for _, device := range []string{tailsplit.MainExit, "office"} {
			for _, approval := range []string{tailsplit.MainExit, "office"} {
				t.Run(mode+"/selected="+device+"/approval="+approval, func(t *testing.T) {
					c, request := loginBrowserStartFixture(t)
					c.mu.Lock()
					c.snapshot.Mode, c.snapshot.Focus = mode, ""
					c.trafficExit = device
					// Snapshot() publishes c.trafficExit. Its backing snapshot can be
					// stale and must not choose the authorization device under the lock.
					c.snapshot.TrafficExit = "office"
					if device == "office" {
						c.snapshot.TrafficExit = tailsplit.MainExit
					}
					c.snapshot.BackendState = "NeedsLogin"
					c.snapshot.Exits = map[string]ExitSnapshot{"office": {BackendState: "NeedsLogin"}}
					if approval == tailsplit.MainExit {
						c.snapshot.BackendState = "NeedsMachineAuth"
					} else {
						c.snapshot.Exits[approval] = ExitSnapshot{BackendState: "NeedsMachineAuth"}
					}
					c.mu.Unlock()
					started := false
					err := c.AuthorizeLoginBrowserStart(request, func() error { started = true; return nil })
					if device == approval {
						if !errors.Is(err, ErrOperationCanceled) || started {
							t.Fatal("the selected traffic device's approval did not block Start", err, started)
						}
					} else if err != nil || !started {
						t.Fatal("an unrelated device's approval blocked the selected traffic device", err, started)
					}
				})
			}
		}
	}
}
