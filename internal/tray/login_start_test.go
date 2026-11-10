package tray

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func loginBrowserStartFixture(t *testing.T) (*Controller, LoginBrowserRequest) {
	t.Helper()
	c := testController(t, "login")
	c.options.ExtraEnv = append(c.options.ExtraEnv, "GORACE=atexit_sleep_ms=0")
	if err := c.ForOperation("helper-owner").StartLogin(); err != nil {
		t.Fatal(err)
	}
	snapshot := waitState(t, c, "login")
	var flow LoginFlow
	flow.RequestOperation("logical-intent", "helper-owner")
	request := flow.NextBrowserRequest(snapshot)
	if request.URL == "" || request.OperationID != "logical-intent" || request.OwnerID != "helper-owner" {
		t.Fatal(request)
	}
	return c, request
}

func browserProcessFixture(ctx context.Context, c *Controller) *exec.Cmd {
	command := exec.CommandContext(ctx, c.options.Helper, c.options.HelperArgs...)
	command.Dir = filepath.Dir(c.options.ConfigPath)
	command.Env = append(os.Environ(), "TAILSPLIT_TEST_HELPER=manual-events", "GORACE=atexit_sleep_ms=0")
	return command
}

func TestLoginBrowserStartRevalidatesLogicalIntentOwnerURLAndCleanup(t *testing.T) {
	for name, change := range map[string]func(*testing.T, *Controller, LoginBrowserRequest){
		"logical-cancel": func(t *testing.T, c *Controller, r LoginBrowserRequest) {
			if err := c.CancelOperation(r.OperationID); err != nil {
				t.Fatal(err)
			}
		},
		"owner-cancel": func(t *testing.T, c *Controller, r LoginBrowserRequest) {
			if err := c.CancelOperation(r.OwnerID); err != nil {
				t.Fatal(err)
			}
		},
		"native-binding-revoked": func(t *testing.T, c *Controller, r LoginBrowserRequest) {
			c.RequestConnectionSetup()
			native := c.Snapshot().ConnectRequest
			if !c.ClaimConnectOperation(native, r.OperationID) {
				t.Fatal("claim failed")
			}
			if err := c.Cancel(native, ""); err != nil {
				t.Fatal(err)
			}
		},
		"later-discovery-owner": func(t *testing.T, c *Controller, _ LoginBrowserRequest) {
			c.mu.Lock()
			c.operationID = "later-discovery"
			c.snapshot.Mode = "discovery"
			c.mu.Unlock()
		},
		"changed-url": func(t *testing.T, c *Controller, _ LoginBrowserRequest) {
			c.mu.Lock()
			c.snapshot.LoginURL = "https://login.tailscale.com/a/replaced"
			c.mu.Unlock()
		},
		"stopping": func(t *testing.T, c *Controller, _ LoginBrowserRequest) {
			c.mu.Lock()
			c.stopping = true
			c.mu.Unlock()
		},
		"failed": func(t *testing.T, c *Controller, _ LoginBrowserRequest) {
			c.mu.Lock()
			c.snapshot.State = "failed"
			c.mu.Unlock()
		},
	} {
		t.Run(name, func(t *testing.T) {
			c, request := loginBrowserStartFixture(t)
			change(t, c, request)
			started := false
			want := ErrOperationCanceled
			if name == "changed-url" {
				want = ErrLoginURLChanged
			}
			if err := c.AuthorizeLoginBrowserStart(request, func() error { started = true; return nil }); !errors.Is(err, want) || started {
				t.Fatal("stale browser dispatch reached Start", err, started)
			}
		})
	}
}

func TestLoginBrowserCancelBeforeSelectionCannotUseStaleSnapshot(t *testing.T) {
	c, old := loginBrowserStartFixture(t)
	snapshot := c.Snapshot()
	var flow LoginFlow
	flow.RequestOperation(old.OperationID, old.OwnerID)
	if err := c.CancelOperation(old.OperationID); err != nil {
		t.Fatal(err)
	}
	// The event-loop inbox has not been applied, so it can still select the old
	// snapshot. The actual launcher boundary must independently reject it.
	selected := flow.NextBrowserRequest(snapshot)
	if selected.URL == "" {
		t.Fatal("fixture did not exercise stale selection")
	}
	if err := c.AuthorizeLoginBrowserStart(selected, func() error { t.Fatal("cancel-before-selection launched browser"); return nil }); !errors.Is(err, ErrOperationCanceled) {
		t.Fatal(err)
	}
}

func TestLoginBrowserCancelAfterSelectionBeforeActualProcessStart(t *testing.T) {
	c, request := loginBrowserStartFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	command := browserProcessFixture(ctx, c)
	selected, release := make(chan struct{}), make(chan struct{})
	releaseStart := sync.OnceFunc(func() { close(release) })
	defer releaseStart()
	result := make(chan error, 1)
	go func() {
		result <- runBrowserCommand(ctx, command, func(start func() error) error {
			close(selected)
			<-release
			return c.AuthorizeLoginBrowserStart(request, start)
		})
	}()
	<-selected
	if err := c.CancelOperation(request.OperationID); err != nil {
		t.Fatal(err)
	}
	releaseStart()
	if err := <-result; !errors.Is(err, ErrOperationCanceled) {
		t.Fatal("selected stale request was admitted", err)
	}
	if command.Process != nil {
		t.Fatal("actual OS process started after cancellation won")
	}
}

func TestLoginBrowserStartFirstCommitsDispatchButNeverHoldsLockDuringWait(t *testing.T) {
	c, request := loginBrowserStartFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	command := browserProcessFixture(ctx, c)
	started, release := make(chan struct{}), make(chan struct{})
	releaseStart := sync.OnceFunc(func() { close(release) })
	defer releaseStart()
	result := make(chan error, 1)
	go func() {
		result <- runBrowserCommand(ctx, command, func(start func() error) error {
			return c.AuthorizeLoginBrowserStart(request, func() error {
				err := start() // The real OS process start, not a reservation.
				close(started)
				<-release
				return err
			})
		})
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("launcher did not reach actual process Start")
	}
	if c.mu.TryLock() {
		c.mu.Unlock()
		t.Fatal("actual process Start did not share cancellation's ownership lock")
	}
	canceled := make(chan error, 1)
	go func() { canceled <- c.CancelOperation(request.OperationID) }()
	releaseStart()
	select {
	case err := <-canceled:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("browser Wait held cancellation's controller lock")
	}
	select {
	case err := <-result:
		t.Fatal("fixture launcher should still be waiting after cancellation", err)
	default:
	}
	cancel()
	select {
	case <-result:
	case <-time.After(5 * time.Second):
		t.Fatal("launcher completion did not finish outside the lock")
	}
	if command.Process == nil {
		t.Fatal("start-first request did not commit an actual dispatch")
	}
}

func TestLoginBrowserURLRefreshRetriesLatestURLWithoutPoisoningLiveOwner(t *testing.T) {
	c, identity := loginBrowserStartFixture(t)
	var flow LoginFlow
	flow.RequestOperation(identity.OperationID, identity.OwnerID)
	old := flow.NextBrowserRequest(c.Snapshot())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	oldCommand := browserProcessFixture(ctx, c)
	selected, release := make(chan struct{}), make(chan struct{})
	releaseStart := sync.OnceFunc(func() { close(release) })
	defer releaseStart()
	result := make(chan error, 1)
	go func() {
		result <- runBrowserCommand(ctx, oldCommand, func(start func() error) error {
			close(selected)
			<-release
			return c.AuthorizeLoginBrowserStart(old, start)
		})
	}()
	<-selected
	const latestURL = "https://login.tailscale.com/a/refreshed-login"
	c.onEvent([]byte(`{"event":"status","backend_state":"NeedsLogin","auth_url":"` + latestURL + `"}`))
	releaseStart()
	err := <-result
	if !errors.Is(err, ErrLoginURLChanged) || errors.Is(err, ErrOperationCanceled) || oldCommand.Process != nil {
		t.Fatal("URL refresh was treated as cancellation or started the stale URL", err)
	}
	flow.Complete(err)
	if flow.Opening() || flow.OperationCanceled(old.OperationID) || flow.OperationCanceled(old.OwnerID) || flow.Message() != "正在准备新授权链接" {
		t.Fatal("stale completion lost pending intent or poisoned its owner", flow.Message())
	}
	latest := flow.NextBrowserRequest(c.Snapshot())
	if latest.URL != latestURL || latest.OperationID != old.OperationID || latest.OwnerID != old.OwnerID {
		t.Fatal("completion did not retry the latest URL with the same identities", latest)
	}
	command := browserProcessFixture(ctx, c)
	// Reuse the existing short-lived discovery fixture as an actual successful
	// launcher process. No browser or network session is opened by the fixture.
	command.Args = append(command.Args, "discover")
	command.Env = append(os.Environ(), "TAILSPLIT_TEST_HELPER=discovery", "GORACE=atexit_sleep_ms=0")
	if err := runBrowserCommand(ctx, command, func(start func() error) error {
		return c.AuthorizeLoginBrowserStart(latest, start)
	}); err != nil || command.Process == nil {
		t.Fatal("latest URL could not pass the actual OS Start boundary", err)
	}
	flow.Complete(nil)
	if got := c.Snapshot(); !got.Active || got.OperationID != old.OwnerID || got.LoginURL != latestURL {
		t.Fatal("URL refresh disrupted the live sign-in helper", got)
	}
	if !flow.RequestOperation(old.OperationID, old.OwnerID) {
		t.Fatal("expected refresh permanently disabled subsequent sign-in")
	}
}

func TestLoginBrowserURLRefreshRetryStillHonorsRealCancellationAndReplacement(t *testing.T) {
	for _, outcome := range []string{"cancel-before-completion", "cancel-before-retry-start", "owner-replaced"} {
		t.Run(outcome, func(t *testing.T) {
			c, identity := loginBrowserStartFixture(t)
			var flow LoginFlow
			flow.RequestOperation(identity.OperationID, identity.OwnerID)
			old := flow.NextBrowserRequest(c.Snapshot())
			c.onEvent([]byte(`{"event":"status","backend_state":"NeedsLogin","auth_url":"https://login.tailscale.com/a/latest"}`))
			stale := c.AuthorizeLoginBrowserStart(old, func() error { t.Fatal("stale URL launched"); return nil })
			if !errors.Is(stale, ErrLoginURLChanged) {
				t.Fatal(stale)
			}
			if outcome == "cancel-before-completion" {
				if err := c.CancelOperation(old.OperationID); err != nil {
					t.Fatal(err)
				}
				flow.CancelOperation(old.OperationID)
				flow.Complete(stale)
				if flow.NextBrowserRequest(c.Snapshot()).URL != "" || flow.Opening() {
					t.Fatal("stale completion revived explicitly canceled intent")
				}
				if err := c.AuthorizeLoginBrowserStart(old, func() error { t.Fatal("cancellation lost to URL freshness"); return nil }); !errors.Is(err, ErrOperationCanceled) {
					t.Fatal(err)
				}
				return
			}
			flow.Complete(stale)
			retry := flow.NextBrowserRequest(c.Snapshot())
			if retry.URL == "" {
				t.Fatal("expected retry was not selected")
			}
			if outcome == "owner-replaced" {
				c.mu.Lock()
				c.operationID = "later-helper"
				c.mu.Unlock()
			} else if err := c.CancelOperation(old.OperationID); err != nil {
				t.Fatal(err)
			}
			err := c.AuthorizeLoginBrowserStart(retry, func() error { t.Fatal("canceled or replaced retry launched"); return nil })
			if !errors.Is(err, ErrOperationCanceled) || errors.Is(err, ErrLoginURLChanged) {
				t.Fatal("real revocation was misclassified as another URL refresh", err)
			}
			flow.Complete(err)
			if flow.pending || flow.Opening() || flow.NextBrowserRequest(c.Snapshot()).URL != "" {
				t.Fatal("real revocation left retry intent active")
			}
			// Guard results end this browser attempt; explicit notifications own
			// local revocation. The controller must still reject the old target.
			if err := c.AuthorizeLoginBrowserStart(retry, func() error { t.Fatal("revoked retry reached Start"); return nil }); !errors.Is(err, ErrOperationCanceled) {
				t.Fatal("controller lost actual revocation", err)
			}
		})
	}
}

func TestLoginBrowserProcessContextIsCheckedAtGuardedStart(t *testing.T) {
	c, request := loginBrowserStartFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	command := browserProcessFixture(ctx, c)
	if err := runBrowserCommand(ctx, command, func(start func() error) error { return c.AuthorizeLoginBrowserStart(request, start) }); !errors.Is(err, context.Canceled) || command.Process != nil {
		t.Fatal("expired worker started an OS process", err)
	}
}
