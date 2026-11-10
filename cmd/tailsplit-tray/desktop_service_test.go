package main

import (
	"errors"
	"slices"
	"testing"
)

func TestDesktopServiceRemovalWaitsForNetworkCleanup(t *testing.T) {
	for _, stopFailure := range []bool{false, true} {
		var calls []string
		stopErr := errors.New("cleanup unfinished")
		err := removeDesktopNetworkService(func() error {
			calls = append(calls, "stop")
			if stopFailure {
				return stopErr
			}
			return nil
		}, func() error { calls = append(calls, "installer"); return nil })
		if stopFailure {
			if !errors.Is(err, stopErr) || !slices.Equal(calls, []string{"stop"}) {
				t.Fatal("uninstaller launched before cleanup", calls, err)
			}
		} else if err != nil || !slices.Equal(calls, []string{"stop", "installer"}) {
			t.Fatal("cleanup/uninstaller order changed", calls, err)
		}
	}
}

func TestDesktopServiceRemovalReportsInstallerFailure(t *testing.T) {
	want := errors.New("installer unavailable")
	if err := removeDesktopNetworkService(func() error { return nil }, func() error { return want }); !errors.Is(err, want) {
		t.Fatal("installer failure lost", err)
	}
}
