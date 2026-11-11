package tray

import (
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCloseForUpdateCancellationDuringHelperCleanup(t *testing.T) {
	for _, target := range []string{"operation", "native-binding", "unrelated"} {
		t.Run(target, func(t *testing.T) {
			c, listener := cancellationBarrierController(t, false)
			c.RequestConnectionSetup()
			request := c.Snapshot().ConnectRequest
			if !c.ClaimConnectOperation(request, "connection") {
				t.Fatal("could not claim native request")
			}
			if err := c.ForOperation("connection").Start(); err != nil {
				t.Fatal(err)
			}
			helper := acceptCancellationBarrier(t, listener, "running")
			release := sync.OnceFunc(func() { _, _ = helper.Write([]byte{1}) })
			t.Cleanup(release)
			before := waitState(t, c, "running")
			result := make(chan error, 1)
			reconnect := false
			go func() {
				result <- c.CloseForUpdate(before.ActiveRevision, func(value bool) error {
					reconnect = value
					return nil
				})
			}()
			waitCancellationPhase(t, helper, "stopping")
			if snapshot := c.Snapshot(); !snapshot.Active || snapshot.State != "stopping" {
				t.Fatal("update lost the helper before actual cleanup", snapshot)
			}
			requestTarget, operationTarget := "", "connection"
			switch target {
			case "native-binding":
				requestTarget, operationTarget = request, "stale-snapshot-operation"
			case "unrelated":
				operationTarget = "unrelated-operation"
			}
			notified, cancellation := make(chan []string, 1), make(chan error, 1)
			go func() {
				cancellation <- c.CancelWithNotify(requestTarget, operationTarget, func(ids []string) { notified <- ids })
			}()
			select {
			case ids := <-notified:
				if target != "unrelated" && !slices.Contains(ids, "connection") {
					t.Fatal("cancellation did not revoke the live owner", ids)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("update held the controller lock during helper cleanup")
			}
			release()
			if err := cancellationResult(t, cancellation); err != nil {
				t.Fatal(err)
			}
			if err := cancellationResult(t, result); err != nil {
				t.Fatal(err)
			}
			if reconnect != (target == "unrelated") {
				t.Fatal("restart did not honor the matching cancellation", reconnect)
			}
			if snapshot := c.Snapshot(); snapshot.Active || snapshot.ActiveRevision != "" {
				t.Fatal("commit ran before helper cleanup finished", snapshot)
			}
		})
	}
}

func TestCloseForUpdateCommitSerializesAndRejectsLateCancellation(t *testing.T) {
	c, listener := cancellationBarrierController(t, false)
	if err := c.ForOperation("connection").Start(); err != nil {
		t.Fatal(err)
	}
	helper := acceptCancellationBarrier(t, listener, "running")
	releaseHelper := sync.OnceFunc(func() { _, _ = helper.Write([]byte{1}) })
	t.Cleanup(releaseHelper)
	before := waitState(t, c, "running")
	entered, releaseCommit := make(chan bool, 1), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(releaseCommit) })
	t.Cleanup(unblock)
	result := make(chan error, 1)
	go func() {
		result <- c.CloseForUpdate(before.ActiveRevision, func(reconnect bool) error {
			entered <- reconnect
			<-releaseCommit // Test barrier for cancellation racing the final commit.
			return nil
		})
	}()
	waitCancellationPhase(t, helper, "stopping")
	releaseHelper()
	select {
	case reconnect := <-entered:
		if !reconnect {
			t.Fatal("healthy connection lost reconnect intent")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("update did not reach commit")
	}
	started, canceled := make(chan struct{}), make(chan error, 1)
	notified := false
	go func() {
		close(started)
		canceled <- c.CancelWithNotify("", "connection", func([]string) { notified = true })
	}()
	<-started
	unblock()
	if err := cancellationResult(t, result); err != nil {
		t.Fatal(err)
	}
	if err := cancellationResult(t, canceled); !errors.Is(err, ErrOperationCanceled) || notified {
		t.Fatal("committed restart acknowledged a cancellation it cannot honor", err, notified)
	}
	if err := c.CancelOperation("connection"); !errors.Is(err, ErrOperationCanceled) {
		t.Fatal("cancellation after successful commit was acknowledged", err)
	}
}

func TestCloseForUpdateRevisionPreflightLeavesHelperRunning(t *testing.T) {
	for _, mismatch := range []string{"empty", "active", "saved", "new-saved", "missing-saved"} {
		t.Run(mismatch, func(t *testing.T) {
			c := testController(t, "normal")
			if err := c.Start(); err != nil {
				t.Fatal(err)
			}
			before := waitState(t, c, "running")
			expected := before.ActiveRevision
			switch mismatch {
			case "empty":
				expected = ""
			case "active":
				expected = "another-revision"
			case "saved", "new-saved":
				if err := os.WriteFile(c.options.ConfigPath, append(slices.Clone(connectionSnapshotConfig), "# externally changed\n"...), 0o600); err != nil {
					t.Fatal(err)
				}
				if mismatch == "new-saved" {
					_, current, err := ReadConfigDocument(c.options.ConfigPath)
					if err != nil {
						t.Fatal(err)
					}
					expected = current
				}
			case "missing-saved":
				if err := os.Remove(c.options.ConfigPath); err != nil {
					t.Fatal(err)
				}
			}
			c.mu.Lock()
			command := c.command
			c.mu.Unlock()
			called := false
			err := c.CloseForUpdate(expected, func(bool) error { called = true; return nil })
			if err == nil || mismatch != "missing-saved" && !errors.Is(err, ErrConfigChanged) || called {
				t.Fatal("stale restart passed preflight", err, called)
			}
			if got := c.Snapshot(); !reflect.DeepEqual(got, before) {
				t.Fatal("revision rejection changed the running snapshot", got, before)
			}
			c.mu.Lock()
			untouched := c.command == command && !c.closed && !c.stopping && c.updateShutdown == nil
			c.mu.Unlock()
			if !untouched {
				t.Fatal("revision rejection started shutting down the helper")
			}
		})
	}
}

func TestCloseForUpdateSavedRevisionChangesDuringCleanup(t *testing.T) {
	for _, change := range []string{"edited", "missing"} {
		t.Run(change, func(t *testing.T) {
			c, listener := cancellationBarrierController(t, false)
			if err := c.Start(); err != nil {
				t.Fatal(err)
			}
			helper := acceptCancellationBarrier(t, listener, "running")
			release := sync.OnceFunc(func() { _, _ = helper.Write([]byte{1}) })
			t.Cleanup(release)
			before := waitState(t, c, "running")
			result := make(chan error, 1)
			called, reconnect := false, true
			go func() {
				result <- c.CloseForUpdate(before.ActiveRevision, func(value bool) error {
					called, reconnect = true, value
					return nil
				})
			}()
			waitCancellationPhase(t, helper, "stopping")
			if change == "edited" {
				if err := os.WriteFile(c.options.ConfigPath, append(slices.Clone(connectionSnapshotConfig), "# changed during cleanup\n"...), 0o600); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Remove(c.options.ConfigPath); err != nil {
				t.Fatal(err)
			}
			release()
			if err := cancellationResult(t, result); err != nil || !called || reconnect {
				t.Fatal("changed saved policy was allowed to reconnect", err, called, reconnect)
			}
		})
	}
}

func TestCloseForUpdateIdleAndNonTrafficHelpersDoNotReconnect(t *testing.T) {
	for _, mode := range []string{"idle", "cached-traffic", "login", "discovery", "failed", "stopping", "logging-out"} {
		t.Run(mode, func(t *testing.T) {
			c := testController(t, "normal")
			switch mode {
			case "login":
				if err := c.StartLogin(); err != nil {
					t.Fatal(err)
				}
				waitState(t, c, "authenticating")
			case "discovery":
				if err := c.RefreshExitNodes(); err != nil {
					t.Fatal(err)
				}
				waitState(t, c, "discovering")
			case "failed", "stopping", "logging-out":
				if err := c.Start(); err != nil {
					t.Fatal(err)
				}
				waitState(t, c, "running")
				c.mu.Lock()
				c.snapshot.State = mode
				c.mu.Unlock()
			case "cached-traffic":
				c.mu.Lock()
				c.snapshot = Snapshot{State: "running", Mode: "tun", RoutingMode: "global"}
				c.mu.Unlock()
			}
			called, reconnect := false, true
			if err := c.CloseForUpdate("", func(value bool) error {
				called, reconnect = true, value
				return nil
			}); err != nil || !called || reconnect {
				t.Fatal("disconnected or non-traffic helper requested restart", err, called, reconnect)
			}
		})
	}
}

func TestCloseForUpdateGlobalSessionRequiresExplicitDisconnect(t *testing.T) {
	for _, state := range []struct{ mode, state, event string }{
		{"tun", "running", `{"event":"status","backend_state":"Running","exit_online":true,"exit_visible":true}`},
		{"tun", "blocked", `{"event":"status","backend_state":"Running","exit_online":false,"exit_visible":true}`},
		{"proxy", "waiting-exit", `{"event":"status","backend_state":"Running","exit_online":false,"exit_visible":true}`},
		{"proxy", "missing-exit", `{"event":"status","backend_state":"Running","exit_online":false,"exit_visible":false}`},
		{"tun", "checking-exit", `{"event":"preflight"}`},
		{"tun", "failed", `{"event":"error","message":"exit unavailable"}`},
	} {
		t.Run(state.state, func(t *testing.T) {
			c := testController(t, "manual-events")
			if err := c.Start(); err != nil {
				t.Fatal(err)
			}
			waitState(t, c, "connecting")
			c.onEvent([]byte(`{"event":"ready","mode":"` + state.mode + `","routing_mode":"global"}`))
			c.onEvent([]byte(state.event))
			before := c.Snapshot()
			if before.State != state.state || !before.Active {
				t.Fatal("fixture did not retain the unhealthy Global session", before)
			}
			called := false
			err := c.CloseForUpdate(before.ActiveRevision, func(bool) error { called = true; return nil })
			if err == nil || !strings.Contains(err.Error(), "reconnect") || !strings.Contains(err.Error(), "explicitly disconnect") || called {
				t.Fatal("unhealthy Global session did not explain recovery before updating", err, called)
			}
			if after := c.Snapshot(); !reflect.DeepEqual(after, before) {
				t.Fatal("update changed the protected Global session", after, before)
			}
			c.mu.Lock()
			untouched := !c.closed && !c.stopping && c.updateShutdown == nil
			c.mu.Unlock()
			if !untouched {
				t.Fatal("update started shutting down Global protection")
			}
		})
	}
}

func TestCloseForUpdateSuccessfulSnapshotMatchesHelperPolicy(t *testing.T) {
	for _, mode := range []string{"proxy", "tun-split"} {
		t.Run(mode, func(t *testing.T) {
			c := testController(t, "normal")
			_, revision, err := ReadConfigDocument(c.options.ConfigPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := c.ForOperation("connection").StartRevision(revision); err != nil {
				t.Fatal(err)
			}
			waitState(t, c, "running")
			if mode == "tun-split" {
				c.onEvent([]byte(`{"event":"ready","mode":"tun","routing_mode":"split"}`))
				c.onEvent([]byte(`{"event":"status","backend_state":"Running","exit_online":true}`))
			}
			before := c.Snapshot()
			c.mu.Lock()
			args := slices.Clone(c.command.Args)
			c.mu.Unlock()
			index := slices.Index(args, "-c")
			if index < 0 || index+1 >= len(args) {
				t.Fatal("helper has no policy snapshot", args)
			}
			_, helperRevision, err := ReadConfigDocument(args[index+1])
			if err != nil || helperRevision != before.ActiveRevision || helperRevision != revision || before.OperationID != "connection" {
				t.Fatal("sample did not identify the running helper's policy", before, helperRevision, err)
			}
			calls := 0
			if err := c.CloseForUpdate(before.ActiveRevision, func(reconnect bool) error {
				calls++
				// The callback already holds c.mu; it must not call Snapshot.
				if !reconnect || c.command != nil || !c.closed || c.activeRevision != "" || c.snapshot.State != "stopped" {
					return errors.New("commit did not receive the healthy session after cleanup")
				}
				return nil
			}); err != nil || calls != 1 {
				t.Fatal("healthy snapshot was not committed exactly once", err, calls)
			}
			if err := c.CloseForUpdate(revision, func(bool) error { calls++; return nil }); !errors.Is(err, ErrOperationCanceled) || calls != 1 {
				t.Fatal("closed controller committed another update", err, calls)
			}
		})
	}
}

func TestCloseForUpdateCommitErrorDoesNotAcknowledgeRestart(t *testing.T) {
	c := testController(t, "normal")
	failure := errors.New("restart commit failed")
	if err := c.CloseForUpdate("", func(bool) error { return failure }); !errors.Is(err, failure) {
		t.Fatal("commit failure was lost", err)
	}
	notified := false
	if err := c.CancelWithNotify("", "connection", func([]string) { notified = true }); err != nil || !notified {
		t.Fatal("failed commit was treated as an irrevocable restart", err, notified)
	}
}
