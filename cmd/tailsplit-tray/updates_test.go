package main

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/idy/tailsplit/internal/appupdate"
	"github.com/idy/tailsplit/internal/configweb"
	"github.com/idy/tailsplit/internal/tray"
)

func TestDesktopUpdateRestoresTrafficIntent(t *testing.T) {
	for _, test := range []struct {
		name     string
		snapshot tray.Snapshot
		argument string
	}{
		{"connected proxy", tray.Snapshot{Active: true, Mode: "proxy", State: "running"}, "--start"},
		{"connected TUN", tray.Snapshot{Active: true, Mode: "tun", State: "running"}, "--start"},
		{"blocked Global", tray.Snapshot{Active: true, Mode: "tun", State: "blocked"}, "--start"},
		{"waiting for exit", tray.Snapshot{Active: true, Mode: "proxy", State: "waiting-exit"}, "--start"},
		{"connecting", tray.Snapshot{Active: true, Mode: "tun", State: "starting"}, "--start"},
		{"traffic authorization", tray.Snapshot{Active: true, Mode: "tun", State: "login"}, "--start"},
		{"stopped", tray.Snapshot{Mode: "tun", State: "stopped"}, "--no-connect"},
		{"disconnecting", tray.Snapshot{Active: true, Mode: "tun", State: "stopping"}, "--no-connect"},
		{"failed", tray.Snapshot{Active: true, Mode: "proxy", State: "failed"}, "--no-connect"},
		{"discovery", tray.Snapshot{Active: true, Mode: "discovery", State: "discovering"}, "--no-connect"},
		{"sign-in only", tray.Snapshot{Active: true, Mode: "login", State: "login"}, "--no-connect"},
		{"logging out", tray.Snapshot{Active: true, Mode: "tun", State: "logging-out"}, "--no-connect"},
		{"service preparation", tray.Snapshot{Active: true, Mode: "service-preparation", State: "authorizing"}, "--no-connect"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := desktopUpdateStartArgument(desktopUpdateReconnect(test.snapshot)); got != test.argument {
				t.Fatalf("got %s, want %s", got, test.argument)
			}
		})
	}
}

type updateFixture struct {
	mu       sync.Mutex
	state    appupdate.State
	check    func(context.Context) error
	download func(context.Context) (appupdate.Package, error)
}

func (f *updateFixture) Snapshot() appupdate.State { f.mu.Lock(); defer f.mu.Unlock(); return f.state }
func (f *updateFixture) SetAutomatic(enabled bool) {
	f.mu.Lock()
	f.state.Automatic = enabled
	f.mu.Unlock()
}
func (f *updateFixture) Check(ctx context.Context) error {
	if f.check != nil {
		return f.check(ctx)
	}
	return nil
}
func (f *updateFixture) Download(ctx context.Context) (appupdate.Package, error) {
	return f.download(ctx)
}

func waitDesktopUpdate(t *testing.T, d *desktopUpdates) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !d.snapshot().Busy {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("desktop update did not finish")
}

func TestDesktopUpdateRequiresConfirmationAndVerifiedDownload(t *testing.T) {
	for _, test := range []struct {
		name                string
		confirm             bool
		downloadError       error
		downloads, installs int
	}{
		{"cancelled", false, nil, 0, 0},
		{"corrupt package", true, errors.New("checksum mismatch"), 1, 0},
		{"verified package", true, nil, 1, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			downloads, installs := 0, 0
			f := &updateFixture{state: appupdate.State{Available: true, LatestVersion: "0.6.3"}}
			f.download = func(context.Context) (appupdate.Package, error) {
				downloads++
				return appupdate.Package{Version: "0.6.3"}, test.downloadError
			}
			d := &desktopUpdates{ctx: context.Background(), updater: f, confirm: func(string) bool { return test.confirm }, install: func(p appupdate.Package) error {
				installs++
				if p.Version != "0.6.3" {
					t.Error("wrong package")
				}
				return nil
			}}
			if err := d.action(context.Background(), "install"); err != nil {
				t.Fatal(err)
			}
			waitDesktopUpdate(t, d)
			if downloads != test.downloads || installs != test.installs {
				t.Fatalf("downloads=%d installs=%d", downloads, installs)
			}
			if test.downloadError != nil && d.snapshot().Error != "checksum mismatch" {
				t.Fatal("download failure lost")
			}
		})
	}
}

func TestDesktopUpdateDoesNotInstallADifferentVersionThanConfirmed(t *testing.T) {
	f := &updateFixture{state: appupdate.State{Available: true, LatestVersion: "0.6.3"}}
	f.download = func(context.Context) (appupdate.Package, error) { return appupdate.Package{Version: "0.6.4"}, nil }
	installed := false
	d := &desktopUpdates{ctx: context.Background(), updater: f, confirm: func(string) bool { return true }, install: func(appupdate.Package) error { installed = true; return nil }}
	if err := d.action(context.Background(), "install"); err != nil {
		t.Fatal(err)
	}
	waitDesktopUpdate(t, d)
	if installed || d.snapshot().Error == "" {
		t.Fatal("installed a version that was not confirmed")
	}
}

func TestDesktopUpdateSerializesRequestsAndLifetime(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	f := &updateFixture{check: func(ctx context.Context) error { close(started); <-ctx.Done(); return ctx.Err() }}
	d := &desktopUpdates{ctx: ctx, updater: f}
	if err := d.action(context.Background(), "check"); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := d.action(context.Background(), "check"); !errors.Is(err, configweb.ErrBusy) {
		t.Fatal("duplicate check accepted:", err)
	}
	cancel()
	waitDesktopUpdate(t, d)
	if err := d.action(context.Background(), "check"); !errors.Is(err, context.Canceled) {
		t.Fatal("operation started after shutdown:", err)
	}
}

func TestDesktopUpdatePreferenceChangesOnlyAfterSaving(t *testing.T) {
	f := &updateFixture{state: appupdate.State{Automatic: true}}
	d := &desktopUpdates{ctx: context.Background(), updater: f}
	d.save = func(context.Context, bool) error {
		requireDesktopUpdateActivity(t, d, appupdate.State{Busy: true})
		if err := d.action(context.Background(), "check"); !errors.Is(err, configweb.ErrBusy) {
			t.Fatal("check accepted during preference save:", err)
		}
		return errors.New("revision conflict")
	}
	if err := d.action(context.Background(), "automatic-off"); err == nil {
		t.Fatal("failed preference save reported success")
	}
	requireDesktopUpdateActivity(t, d, appupdate.State{})
	if !f.Snapshot().Automatic {
		t.Fatal("failed save changed the active schedule")
	}
	d.save = func(context.Context, bool) error {
		requireDesktopUpdateActivity(t, d, appupdate.State{Busy: true})
		if !f.Snapshot().Automatic {
			t.Fatal("preference changed before saving")
		}
		return nil
	}
	if err := d.action(context.Background(), "automatic-off"); err != nil {
		t.Fatal(err)
	}
	requireDesktopUpdateActivity(t, d, appupdate.State{})
	if f.Snapshot().Automatic {
		t.Fatal("successful save did not disable checking")
	}
}

func requireDesktopUpdateActivity(t *testing.T, d *desktopUpdates, want appupdate.State) appupdate.State {
	t.Helper()
	got := d.snapshot()
	if got.Busy != want.Busy || got.Checking != want.Checking || got.Confirming != want.Confirming ||
		got.Downloading != want.Downloading || got.Installing != want.Installing {
		t.Fatalf("unexpected update activity: %+v; want %+v", got, want)
	}
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["busy"] != want.Busy || fields["confirming"] != want.Confirming {
		t.Fatalf("missing or incorrect activity in JSON: %s", data)
	}
	return got
}

func waitDesktopUpdateSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatal("desktop update did not reach the expected phase")
	}
}

func TestDesktopUpdatePublishesCheckBeforeWorkerState(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{"success", nil},
		{"failure", errors.New("latest check failed")},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			started, proceed := make(chan struct{}), make(chan struct{})
			f := &updateFixture{check: func(ctx context.Context) error {
				close(started)
				select {
				case <-proceed:
					return test.err
				case <-ctx.Done():
					return ctx.Err()
				}
			}}
			d := &desktopUpdates{ctx: ctx, updater: f, err: "previous failure"}
			t.Cleanup(func() { cancel(); waitDesktopUpdate(t, d) })
			if err := d.action(context.Background(), "check"); err != nil {
				t.Fatal(err)
			}
			state := requireDesktopUpdateActivity(t, d, appupdate.State{Busy: true, Checking: true})
			if state.Error != "" {
				t.Fatal("new check retained the previous coordinator error:", state.Error)
			}
			waitDesktopUpdateSignal(t, started)
			if f.Snapshot().Checking {
				t.Fatal("fixture unexpectedly provided the checking status")
			}
			requireDesktopUpdateActivity(t, d, appupdate.State{Busy: true, Checking: true})
			close(proceed)
			waitDesktopUpdate(t, d)
			state = requireDesktopUpdateActivity(t, d, appupdate.State{})
			if test.err != nil && state.Error != test.err.Error() {
				t.Fatal("check failure was hidden:", state.Error)
			}
		})
	}
}

func TestDesktopUpdatePublishesConfirmationDownloadAndInstallPhases(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	checkStarted, confirmStarted := make(chan struct{}), make(chan struct{})
	downloadStarted, installStarted := make(chan struct{}), make(chan struct{})
	checkProceed, downloadProceed, installProceed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	decision := make(chan bool, 1)
	confirmedVersion := ""
	f := &updateFixture{state: appupdate.State{Available: true, LatestVersion: "0.7.0"}}
	f.check = func(ctx context.Context) error {
		close(checkStarted)
		select {
		case <-checkProceed:
			f.mu.Lock()
			f.state.LatestVersion = "0.8.0"
			f.mu.Unlock()
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f.download = func(ctx context.Context) (appupdate.Package, error) {
		close(downloadStarted)
		select {
		case <-downloadProceed:
			return appupdate.Package{Version: "0.8.0"}, nil
		case <-ctx.Done():
			return appupdate.Package{}, ctx.Err()
		}
	}
	d := &desktopUpdates{ctx: ctx, updater: f,
		confirm: func(version string) bool {
			confirmedVersion = version
			close(confirmStarted)
			select {
			case answer := <-decision:
				return answer
			case <-ctx.Done():
				return false
			}
		},
		install: func(appupdate.Package) error {
			close(installStarted)
			select {
			case <-installProceed:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
	t.Cleanup(func() {
		cancel()
		f.mu.Lock()
		f.state.Checking = false
		f.mu.Unlock()
		waitDesktopUpdate(t, d)
	})
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	defer cancelRequest()
	if err := d.action(requestCtx, "install"); err != nil {
		t.Fatal(err)
	}
	// Completing the POST must not cancel the accepted desktop operation.
	cancelRequest()
	requireDesktopUpdateActivity(t, d, appupdate.State{Busy: true, Checking: true})
	waitDesktopUpdateSignal(t, checkStarted)
	close(checkProceed)
	waitDesktopUpdateSignal(t, confirmStarted)
	state := requireDesktopUpdateActivity(t, d, appupdate.State{Busy: true, Confirming: true})
	if confirmedVersion != "0.8.0" || state.LatestVersion != confirmedVersion {
		t.Fatalf("confirmation did not use the refreshed version: confirmed=%s state=%+v", confirmedVersion, state)
	}
	for _, action := range []string{"check", "install", "automatic-on", "automatic-off"} {
		if err := d.action(context.Background(), action); !errors.Is(err, configweb.ErrBusy) {
			t.Fatalf("%s accepted while confirmation was blocked: %v", action, err)
		}
	}
	// A scheduled updater check remains visible while the coordinator waits.
	f.mu.Lock()
	f.state.Checking = true
	f.mu.Unlock()
	requireDesktopUpdateActivity(t, d, appupdate.State{Busy: true, Checking: true, Confirming: true})
	f.mu.Lock()
	f.state.Checking = false
	f.mu.Unlock()
	decision <- true
	waitDesktopUpdateSignal(t, downloadStarted)
	requireDesktopUpdateActivity(t, d, appupdate.State{Busy: true, Downloading: true})
	close(downloadProceed)
	waitDesktopUpdateSignal(t, installStarted)
	requireDesktopUpdateActivity(t, d, appupdate.State{Busy: true, Installing: true})
	close(installProceed)
	waitDesktopUpdate(t, d)
	state = requireDesktopUpdateActivity(t, d, appupdate.State{})
	if state.Error != "" {
		t.Fatal("successful update reported an error:", state.Error)
	}
}

func TestDesktopUpdateBlockedConfirmationCancellation(t *testing.T) {
	for _, test := range []struct {
		name     string
		shutdown bool
		accept   bool
	}{
		{name: "declined"},
		{name: "shutdown before acceptance", shutdown: true, accept: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			started, decision := make(chan struct{}), make(chan bool, 1)
			downloads, installs := 0, 0
			f := &updateFixture{state: appupdate.State{Available: true, LatestVersion: "0.8.0"}}
			f.download = func(context.Context) (appupdate.Package, error) {
				downloads++
				return appupdate.Package{Version: "0.8.0"}, nil
			}
			d := &desktopUpdates{ctx: ctx, updater: f,
				confirm: func(string) bool { close(started); return <-decision },
				install: func(appupdate.Package) error { installs++; return nil },
			}
			t.Cleanup(func() {
				cancel()
				select {
				case decision <- false:
				default:
				}
				waitDesktopUpdate(t, d)
			})
			if err := d.action(context.Background(), "install"); err != nil {
				t.Fatal(err)
			}
			waitDesktopUpdateSignal(t, started)
			requireDesktopUpdateActivity(t, d, appupdate.State{Busy: true, Confirming: true})
			if test.shutdown {
				cancel()
				// The callback still owns the outstanding confirmation until it returns.
				requireDesktopUpdateActivity(t, d, appupdate.State{Busy: true, Confirming: true})
			}
			decision <- test.accept
			waitDesktopUpdate(t, d)
			state := requireDesktopUpdateActivity(t, d, appupdate.State{})
			if downloads != 0 || installs != 0 || state.Error != "" {
				t.Fatalf("cancelled confirmation continued: downloads=%d installs=%d state=%+v", downloads, installs, state)
			}
		})
	}
}

func TestDesktopUpdateDownloadFailureAndCancellationPhases(t *testing.T) {
	for _, test := range []struct {
		name                        string
		downloadError, installError error
		cancelDuring, cancelAfter   bool
		installs                    int
	}{
		{name: "verification failure", downloadError: errors.New("checksum mismatch")},
		{name: "shutdown during download", cancelDuring: true},
		{name: "shutdown before install", cancelAfter: true},
		{name: "installer failure", installError: errors.New("installer rejected package"), installs: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			started, proceed := make(chan struct{}), make(chan struct{})
			installs := 0
			f := &updateFixture{state: appupdate.State{Available: true, LatestVersion: "0.8.0"}}
			f.download = func(ctx context.Context) (appupdate.Package, error) {
				close(started)
				select {
				case <-proceed:
					if test.cancelAfter {
						cancel()
					}
					return appupdate.Package{Version: "0.8.0"}, test.downloadError
				case <-ctx.Done():
					return appupdate.Package{}, ctx.Err()
				}
			}
			d := &desktopUpdates{ctx: ctx, updater: f,
				confirm: func(string) bool { return true },
				install: func(appupdate.Package) error { installs++; return test.installError },
			}
			t.Cleanup(func() { cancel(); waitDesktopUpdate(t, d) })
			if err := d.action(context.Background(), "install"); err != nil {
				t.Fatal(err)
			}
			waitDesktopUpdateSignal(t, started)
			requireDesktopUpdateActivity(t, d, appupdate.State{Busy: true, Downloading: true})
			if test.cancelDuring {
				cancel()
			} else {
				close(proceed)
			}
			waitDesktopUpdate(t, d)
			state := requireDesktopUpdateActivity(t, d, appupdate.State{})
			wantError := ""
			if test.downloadError != nil {
				wantError = test.downloadError.Error()
			} else if test.installError != nil {
				wantError = test.installError.Error()
			}
			if installs != test.installs || state.Error != wantError {
				t.Fatalf("unexpected download result: installs=%d state=%+v", installs, state)
			}
		})
	}
}

func TestDesktopUpdatePreservesUpdaterActivity(t *testing.T) {
	for _, state := range []appupdate.State{{Checking: true}, {Downloading: true}} {
		f := &updateFixture{state: state}
		d := &desktopUpdates{ctx: context.Background(), updater: f}
		state.Busy = true
		requireDesktopUpdateActivity(t, d, state)
		if source := f.Snapshot(); source.Busy || source.Confirming || source.Installing {
			t.Fatalf("coordinator mutated its source snapshot: %+v", source)
		}
	}
}
