package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/idy/tailsplit/internal/appupdate"
	"github.com/idy/tailsplit/internal/configweb"
)

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
func (f *updateFixture) Check(ctx context.Context) error { return f.check(ctx) }
func (f *updateFixture) Download(ctx context.Context) (appupdate.Package, error) {
	return f.download(ctx)
}

func waitDesktopUpdate(t *testing.T, d *desktopUpdates) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		d.mu.Lock()
		busy := d.busy
		d.mu.Unlock()
		if !busy {
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
	d := &desktopUpdates{ctx: context.Background(), updater: f, save: func(context.Context, bool) error { return errors.New("revision conflict") }}
	if err := d.action(context.Background(), "automatic-off"); err == nil {
		t.Fatal("failed preference save reported success")
	}
	if !f.Snapshot().Automatic {
		t.Fatal("failed save changed the active schedule")
	}
	d.save = func(context.Context, bool) error { return nil }
	if err := d.action(context.Background(), "automatic-off"); err != nil {
		t.Fatal(err)
	}
	if f.Snapshot().Automatic {
		t.Fatal("successful save did not disable checking")
	}
}
