package main

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/idy/tailsplit/internal/appupdate"
)

func TestInstallRefreshesLatestBeforeConfirmingInsteadOfUsingStaleOffer(t *testing.T) {
	for _, test := range []struct {
		name      string
		available bool
		checkErr  error
		want      []string
	}{
		{"skip intermediate releases", true, nil, []string{"check", "confirm:0.8.0", "download:0.8.0", "install:0.8.0"}},
		{"withdrawn offer", false, nil, []string{"check"}},
		{"recheck failure never installs stale offer", true, errors.New("latest check failed"), []string{"check"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls []string
			f := &updateFixture{state: appupdate.State{CurrentVersion: "0.6.1", LatestVersion: "0.7.0", Available: true}}
			f.check = func(context.Context) error {
				calls = append(calls, "check")
				if test.checkErr != nil {
					return test.checkErr
				}
				f.mu.Lock()
				f.state.LatestVersion, f.state.Available = "0.8.0", test.available
				f.mu.Unlock()
				return nil
			}
			f.download = func(context.Context) (appupdate.Package, error) {
				version := f.Snapshot().LatestVersion
				calls = append(calls, "download:"+version)
				return appupdate.Package{Version: version}, nil
			}
			d := &desktopUpdates{ctx: context.Background(), updater: f,
				confirm: func(version string) bool { calls = append(calls, "confirm:"+version); return true },
				install: func(pkg appupdate.Package) error { calls = append(calls, "install:"+pkg.Version); return nil },
			}
			if err := d.action(context.Background(), "install"); err != nil {
				t.Fatal(err)
			}
			waitDesktopUpdate(t, d)
			if !reflect.DeepEqual(calls, test.want) {
				t.Fatalf("update sequence = %v, want %v", calls, test.want)
			}
			state := d.snapshot()
			if test.checkErr != nil && state.Error != test.checkErr.Error() {
				t.Fatal("latest check failure was hidden:", state.Error)
			}
			if state.Busy || state.Checking || state.Confirming || state.Downloading || state.Installing {
				t.Fatalf("update remained active after completion: %+v", state)
			}
		})
	}
}
