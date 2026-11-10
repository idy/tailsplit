package tray

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/idy/tailsplit"
)

func TestCanceledOperationRejectsAdmissionBeforeItsFirstAction(t *testing.T) {
	c := testController(t, "normal")
	if err := c.CancelOperation("not-yet-admitted"); err != nil {
		t.Fatal(err)
	}
	op := c.ForOperation("not-yet-admitted")
	_, revision, _ := ReadConfigDocument(c.options.ConfigPath)
	called := false
	for name, action := range map[string]func() error{
		"connect":          op.Start,
		"revision connect": func() error { return op.StartRevision(revision) },
		"login":            op.StartLogin,
		"focused login":    func() error { return op.StartExitLogin("office") },
		"discovery":        op.RefreshExitNodes,
		"add":              func() error { return op.AddExit("office", revision) },
		"prepare": func() error {
			return op.PrepareService(context.Background(), func(context.Context) error { called = true; return nil })
		},
		"retry": op.Restart,
		"save":  func() error { return c.ReconfigureOperation(op.ID, nil, func() error { called = true; return nil }) },
	} {
		if err := action(); !errors.Is(err, ErrOperationCanceled) {
			t.Fatalf("%s admitted canceled operation: %v", name, err)
		}
	}
	if got := c.Snapshot(); got.Active || got.OperationID != "" || called {
		t.Fatal("cancellation admitted work or changed ownership", got, called)
	}
	if err := c.ForOperation("explicit-retry").Start(); err != nil {
		t.Fatal("fresh retry was rejected", err)
	}
	if c.Snapshot().OperationID != "explicit-retry" {
		t.Fatal(c.Snapshot())
	}
}

func TestOperationCancellationCoversServicePreparationAndStartGap(t *testing.T) {
	for _, during := range []bool{true, false} {
		t.Run(map[bool]string{true: "preparing", false: "prepared-before-start"}[during], func(t *testing.T) {
			c := testController(t, "normal")
			op := c.ForOperation("setup")
			if during {
				ready, result := make(chan struct{}), make(chan error, 1)
				go func() {
					result <- op.PrepareService(context.Background(), func(ctx context.Context) error { close(ready); <-ctx.Done(); return ctx.Err() })
				}()
				<-ready
				if got := c.Snapshot(); got.OperationID != op.ID || !got.PreparingService() {
					t.Fatal(got)
				}
				if err := c.CancelOperation(op.ID); err != nil {
					t.Fatal(err)
				}
				if err := <-result; !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			} else {
				if err := op.PrepareService(context.Background(), func(context.Context) error { return nil }); err != nil {
					t.Fatal(err)
				}
				if got := c.Snapshot(); got.OperationID != op.ID || got.Active {
					t.Fatal("preparation lost logical owner", got)
				}
				if err := c.CancelOperation(op.ID); err != nil {
					t.Fatal(err)
				}
			}
			if err := op.StartLogin(); !errors.Is(err, ErrOperationCanceled) {
				t.Fatal("canceled preparation restarted login", err)
			}
			if err := op.Start(); !errors.Is(err, ErrOperationCanceled) {
				t.Fatal("canceled preparation started traffic", err)
			}
			if c.Snapshot().Active {
				t.Fatal(c.Snapshot())
			}
			if err := c.ForOperation("retry").Start(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCancelCapturedHelperRetainsLeaseAndNeverStopsNewOwner(t *testing.T) {
	c := testController(t, "cleanup")
	directory := t.TempDir()
	state, canceled, release := filepath.Join(directory, "state"), filepath.Join(directory, "canceled"), filepath.Join(directory, "release")
	defer func() { _ = os.WriteFile(release, nil, 0o600) }()
	c.options.ExtraEnv = append(c.options.ExtraEnv, "GORACE=atexit_sleep_ms=0", "TAILSPLIT_TEST_LEASE="+state, "TAILSPLIT_TEST_CANCELED="+canceled, "TAILSPLIT_TEST_RELEASE="+release)
	c.RequestConnectionSetup()
	oldRequest := c.Snapshot().ConnectRequest
	if !c.ClaimConnectOperation(oldRequest, oldRequest) {
		t.Fatal("claim failed")
	}
	if err := c.ForOperation(oldRequest).Start(); err != nil {
		t.Fatal(err)
	}
	waitState(t, c, "running")
	c.mu.Lock()
	lateWaiter := c.captureStopLocked()
	c.mu.Unlock()
	stopped := make(chan error, 1)
	go func() { stopped <- c.Cancel(oldRequest, oldRequest) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(canceled); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cancel never signaled its captured helper")
		}
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case err := <-stopped:
		t.Fatal("cancel returned before lease cleanup", err)
	default:
	}
	if unlock, err := tailsplit.LockSavedState(state); err == nil {
		_ = unlock()
		t.Fatal("cancel released real lease before cleanup")
	}
	if err := c.ForOperation("new-owner").Start(); err == nil {
		t.Fatal("new helper entered during cleanup")
	}
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	if err := c.ForOperation("new-owner").Start(); err != nil {
		t.Fatal(err)
	}
	waitState(t, c, "running")
	c.mu.Lock()
	command := c.command
	c.mu.Unlock()
	c.RequestConnectionSetup()
	newRequest := c.Snapshot().ConnectRequest
	var retries sync.WaitGroup
	for range 16 {
		retries.Go(func() {
			if err := c.Cancel(oldRequest, oldRequest); err != nil {
				t.Error(err)
			}
		})
	}
	retries.Wait()
	if c.ClaimConnectOperation(oldRequest, "late-rebound-owner") {
		t.Fatal("late claim revived old native intent")
	}
	if err := c.ForOperation(oldRequest).Start(); !errors.Is(err, ErrOperationCanceled) {
		t.Fatal("late action revived canceled operation", err)
	}
	c.mu.Lock()
	unchanged := c.command == command && !c.stopping
	c.mu.Unlock()
	if got := c.Snapshot(); !unchanged || !got.Active || got.OperationID != "new-owner" || got.ConnectRequest != newRequest {
		t.Fatal("stale cancellation changed new owner", got)
	}
	if unlock, err := tailsplit.LockSavedState(state); err == nil {
		_ = unlock()
		t.Fatal("stale cancel released newer helper's real lease")
	}
	if err := os.Remove(release); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(canceled); err != nil {
		t.Fatal(err)
	}
	go func() { stopped <- c.CancelOperation("new-owner") }()
	deadline = time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(canceled); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("new helper did not enter cleanup")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := c.waitStopped(lateWaiter); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	stillStopping := c.stopping && c.command == command
	c.mu.Unlock()
	if !stillStopping {
		t.Fatal("old Stop waiter cleared newer cleanup ownership")
	}
	if err := c.ForOperation("third-owner").Start(); err == nil {
		t.Fatal("old waiter reopened admission during new cleanup")
	}
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
}

func TestRepeatedCancelTimeoutsShareCleanupCompletionAndEventuallyReleaseGate(t *testing.T) {
	c := testController(t, "normal")
	c.stopWait = time.Millisecond
	cleanup := make(chan struct{})
	c.mu.Lock()
	c.operationID = "cleanup-owner"
	c.pingCancel, c.pingDone = func() {}, cleanup
	c.mu.Unlock()
	finish := sync.OnceFunc(func() {
		c.mu.Lock()
		c.pingCancel, c.pingDone = nil, nil
		close(cleanup)
		c.mu.Unlock()
	})
	defer finish()
	var shared *stopCompletion
	for range 12 {
		if err := c.CancelOperation("cleanup-owner"); !errors.Is(err, tailsplit.ErrPingBusy) {
			t.Fatal("cancel lost ongoing cleanup", err)
		}
		c.mu.Lock()
		completion, stopping := c.stopCompletion, c.stopping
		c.mu.Unlock()
		if shared == nil {
			shared = completion
		}
		if completion != shared || !stopping {
			t.Fatal("timeout retry created independent cleanup ownership")
		}
	}
	if err := c.ForOperation("new-owner").Start(); err == nil {
		t.Fatal("timeouts reopened helper admission")
	}
	finish()
	select {
	case <-shared.done:
	case <-time.After(time.Second):
		t.Fatal("completed cleanup did not release shared gate")
	}
	c.stopWait = 10 * time.Second
	if err := c.ForOperation("new-owner").Start(); err != nil {
		t.Fatal("completed cleanup left admission blocked", err)
	}
}

func TestSignalErrorStillReleasesStopGateWhenCapturedCleanupCompletes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows cancellation uses a best-effort lease pipe instead of signals")
	}
	c := testController(t, "normal")
	// A separate, released handle for this process fails locally before any
	// signal can be delivered. It does not affect the test process or a helper.
	process, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Release(); err != nil {
		t.Fatal(err)
	}
	command := &exec.Cmd{Process: process}
	done := make(chan struct{})
	c.mu.Lock()
	c.command, c.done = command, done
	c.operationID = "signal-error-owner"
	c.mu.Unlock()
	finish := sync.OnceFunc(func() {
		c.mu.Lock()
		c.command = nil
		close(done)
		c.mu.Unlock()
	})
	defer finish()
	if err := c.CancelOperation("signal-error-owner"); err == nil || errors.Is(err, os.ErrProcessDone) {
		t.Fatal("fixture did not report a signal error", err)
	}
	c.mu.Lock()
	completion, stopping := c.stopCompletion, c.stopping
	c.mu.Unlock()
	if !stopping {
		t.Fatal("signal error released cleanup ownership before completion")
	}
	finish()
	select {
	case <-completion.done:
	case <-time.After(time.Second):
		t.Fatal("signal error left stop gate permanently blocked")
	}
	if err := c.ForOperation("after-cleanup").Start(); err != nil {
		t.Fatal("signal error stranded admission after cleanup", err)
	}
}

func TestCancelDuringReconfigureUpdateCannotRestartCapturedOwner(t *testing.T) {
	for _, legacyStop := range []bool{false, true} {
		t.Run(map[bool]string{false: "guarded-cancel", true: "legacy-stop"}[legacyStop], func(t *testing.T) {
			c := testController(t, "normal")
			if err := c.ForOperation("connection").Start(); err != nil {
				t.Fatal(err)
			}
			waitState(t, c, "running")
			updating, release, result := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			go func() {
				result <- c.ReconfigureOperation("connection", nil, func() error { close(updating); <-release; return nil })
			}()
			<-updating
			if got := c.Snapshot(); got.Active || got.OperationID != "connection" {
				t.Fatal("save lost captured owner", got)
			}
			var err error
			if legacyStop {
				err = c.Stop()
			} else {
				err = c.CancelOperation("connection")
			}
			close(release)
			if err != nil {
				t.Fatal(err)
			}
			if err := <-result; !errors.Is(err, ErrOperationCanceled) {
				t.Fatal("save restart ignored cancellation", err)
			}
			if c.Snapshot().Active {
				t.Fatal("save restarted canceled connection", c.Snapshot())
			}
			if err := c.ForOperation("new-connection").Start(); err != nil {
				t.Fatal(err)
			}
			waitState(t, c, "running")
			updated := false
			if err := c.ReconfigureOperation("connection", nil, func() error { updated = true; return nil }); !errors.Is(err, ErrOperationCanceled) || updated {
				t.Fatal("stale save affected new connection", err, updated)
			}
		})
	}
}

func TestInternalSaveRestartInheritsOperationAndNativeClaimDoesNotReplaceIt(t *testing.T) {
	c := testController(t, "normal")
	if err := c.ForOperation("connection").Start(); err != nil {
		t.Fatal(err)
	}
	waitState(t, c, "running")
	if err := c.Cancel("connection", ""); err != nil {
		t.Fatal(err)
	}
	if err := c.ForOperation("connection").Check(); err != nil {
		t.Fatal("unknown native request revoked an unrelated browser operation", err)
	}
	if err := c.ReconfigureChecked(nil, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	waitState(t, c, "running")
	c.RequestConnectionSetup()
	request := c.Snapshot().ConnectRequest
	if !c.ClaimConnectOperation(request, request) {
		t.Fatal("claim failed")
	}
	if got := c.Snapshot(); got.OperationID != "connection" {
		t.Fatal("native claim replaced active owner", got)
	}
	if err := c.Cancel(request, request); err != nil {
		t.Fatal(err)
	}
	if got := c.Snapshot(); !got.Active || got.OperationID != "connection" {
		t.Fatal("ignored native flow stopped live connection", got)
	}
	if err := c.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := c.ReconfigureChecked(nil, func() error { return nil }); err != nil {
		t.Fatal("legacy idle save failed after disconnect", err)
	}
}

func TestRequestOnlyCancelRevokesClaimedOperationWithoutStoppingUnrelatedHelper(t *testing.T) {
	for _, customBinding := range []bool{false, true} {
		t.Run(map[bool]string{false: "request-is-operation", true: "retained-custom-binding"}[customBinding], func(t *testing.T) {
			c := testController(t, "normal")
			if err := c.ForOperation("unrelated-live-connection").Start(); err != nil {
				t.Fatal(err)
			}
			waitState(t, c, "running")
			c.RequestConnectionSetup()
			request := c.Snapshot().ConnectRequest
			id := request
			if customBinding {
				id = "claimed-logical-operation"
			}
			if !c.ClaimConnectOperation(request, id) {
				t.Fatal("claim failed")
			}
			c.RequestConnectionSetup()
			newRequest := c.Snapshot().ConnectRequest
			if err := c.Cancel(request, ""); err != nil {
				t.Fatal(err)
			}
			if err := c.ForOperation(id).Start(); !errors.Is(err, ErrOperationCanceled) {
				t.Fatal("request-only cancellation admitted a delayed claimed action", err)
			}
			if got := c.Snapshot(); !got.Active || got.OperationID != "unrelated-live-connection" || got.ConnectRequest != newRequest {
				t.Fatal("request-only cancellation changed unrelated owner", got)
			}
			if c.ClaimConnectOperation(request, "late-rebind") {
				t.Fatal("request was rebound after revocation")
			}
		})
	}
}

func TestRequestOnlyCancelStopsItsAlreadyAdmittedNativeOwner(t *testing.T) {
	c := testController(t, "normal")
	c.RequestConnectionSetup()
	id := c.Snapshot().ConnectRequest
	if !c.ClaimConnectOperation(id, id) {
		t.Fatal("claim failed")
	}
	if err := c.ForOperation(id).Start(); err != nil {
		t.Fatal(err)
	}
	waitState(t, c, "running")
	if err := c.Cancel(id, ""); err != nil {
		t.Fatal(err)
	}
	if got := c.Snapshot(); got.Active || got.OperationID != id {
		t.Fatal("request-only cancellation failed to stop its retained native owner", got)
	}
	if err := c.ForOperation(id).Start(); !errors.Is(err, ErrOperationCanceled) {
		t.Fatal("revoked binding still admitted work", err)
	}
	if err := c.CancelOperation(id); err != nil {
		t.Fatal(err)
	}
	if c.Snapshot().Active {
		t.Fatal("explicit operation cancellation did not stop owner")
	}
}

func TestNativeBindingCancelStopsBoundOwnerDespiteStaleSnapshotAndPreservesNewerOwner(t *testing.T) {
	for _, newer := range []bool{false, true} {
		t.Run(map[bool]string{false: "bound-active", true: "unrelated-newer"}[newer], func(t *testing.T) {
			c := testController(t, "normal")
			c.RequestConnectionSetup()
			request := c.Snapshot().ConnectRequest
			if !c.ClaimConnectOperation(request, "bound-operation") {
				t.Fatal("claim failed")
			}
			if err := c.ForOperation("bound-operation").Start(); err != nil {
				t.Fatal(err)
			}
			waitState(t, c, "running")
			if newer {
				if err := c.Stop(); err != nil {
					t.Fatal(err)
				}
				if err := c.ForOperation("newer-operation").Start(); err != nil {
					t.Fatal(err)
				}
				waitState(t, c, "running")
			}
			c.RequestConnectionSetup()
			latest := c.Snapshot().ConnectRequest
			var notified []string
			if err := c.CancelWithNotify(request, "stale-snapshot-operation", func(ids []string) { notified = ids }); err != nil {
				t.Fatal(err)
			}
			got := c.Snapshot()
			if got.Active != newer || got.ConnectRequest != latest || newer && got.OperationID != "newer-operation" {
				t.Fatal("native binding cancellation selected wrong owner", got)
			}
			for _, id := range []string{request, "bound-operation", "stale-snapshot-operation"} {
				found := false
				for _, target := range notified {
					if target == id {
						found = true
					}
				}
				if !found {
					t.Fatal("host missed effective cancellation target", id, notified)
				}
			}
			if err := c.ForOperation("bound-operation").Start(); !errors.Is(err, ErrOperationCanceled) {
				t.Fatal("bound owner was readmitted", err)
			}
		})
	}
}

func TestReplacedPreparedOperationCannotReclaimNewerConnection(t *testing.T) {
	c := testController(t, "normal")
	old := c.ForOperation("old-preparation")
	if err := old.PrepareService(context.Background(), func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := c.ForOperation("new-connection").Start(); err != nil {
		t.Fatal(err)
	}
	if err := old.Start(); !errors.Is(err, ErrOperationCanceled) {
		t.Fatal("old preparation reclaimed ownership", err)
	}
	if err := old.Stop(); !errors.Is(err, ErrOperationCanceled) {
		t.Fatal("old phase stop affected new helper", err)
	}
	if got := c.Snapshot(); !got.Active || got.OperationID != "new-connection" {
		t.Fatal(got)
	}
}
