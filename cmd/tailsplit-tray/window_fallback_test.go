package main

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/idy/tailsplit/internal/tray"
)

func TestConfigurationFallbackHandlesAsyncFailureOncePerShow(t *testing.T) {
	var opens atomic.Int32
	fallback := configurationFallback{launch: func() { opens.Add(1) }}
	fallback.nativeFailed(false)
	if opens.Load() != 0 {
		t.Fatal("background native failure opened a browser")
	}
	// Show returned while the native page was loading; Run fails later.
	fallback.beginShow()
	fallback.nativeFailed(true)
	if opens.Load() != 1 {
		t.Fatal("asynchronous visible-window failure did not open fallback")
	}
	fallback.open()
	if opens.Load() != 1 {
		t.Fatal("Show and Run failures opened duplicate browser tabs")
	}
	// A user can reopen the browser after closing its previous fallback tab.
	fallback.beginShow()
	fallback.open()
	if opens.Load() != 2 {
		t.Fatal("a later explicit Show could not reopen fallback")
	}
	fallback.beginShow()
	fallback.nativeFailed(false)
	if opens.Load() != 2 {
		t.Fatal("a canceled visibility request opened fallback")
	}
}

func TestConfigurationWindowFailurePreservesGlobalControllerAndRecoveryLifetime(t *testing.T) {
	ctx, quit := context.WithCancel(context.Background())
	defer quit()
	recovery, stopRecovery := context.WithCancel(context.Background())
	defer stopRecovery()
	controller := &desktopLoginFixture{snapshot: tray.Snapshot{State: "running", Active: true, Mode: "tun", RoutingMode: "global", OperationID: "global-owner"}}
	before := controller.Snapshot()
	opens, reports := 0, 0
	assertRetained := func() {
		t.Helper()
		if controller.stops != 0 || !reflect.DeepEqual(controller.Snapshot(), before) || recovery.Err() != nil {
			t.Fatal("view failure tore down Global traffic or pending native recovery", controller.Snapshot(), recovery.Err())
		}
	}
	fallback := configurationFallback{ctx: ctx, launch: func() { assertRetained(); opens++ }}
	report := func(error) { reports++ }
	failed := errors.New("native readiness failed")
	fallback.nativeFailure(failed, false, report)
	if opens != 0 {
		t.Fatal("background view failure opened a browser")
	}
	fallback.beginShow()
	fallback.nativeFailure(nil, true, report) // Show is loading; Run has not failed yet.
	fallback.nativeFailure(failed, true, report)
	fallback.nativeFailure(failed, true, report) // A synchronous Show error joins Run's failure.
	if opens != 1 || ctx.Err() != nil {
		t.Fatal("failure handoff duplicated fallback or ended app lifetime", opens, ctx.Err())
	}
	assertRetained()
	fallback.beginShow()
	fallback.nativeFailure(failed, true, report)
	if opens != 2 {
		t.Fatal("later explicit Show could not retry browser fallback", opens)
	}
	quit()
	previousReports := reports
	fallback.beginShow()
	fallback.nativeFailure(failed, true, report)
	if opens != 2 || reports != previousReports {
		t.Fatal("late view callback reopened fallback during shutdown", opens, reports)
	}
	assertRetained()
}

func TestConfigurationWindowFailureChecksLifetimeAgainAfterReporting(t *testing.T) {
	ctx, quit := context.WithCancel(context.Background())
	defer quit()
	opens := 0
	fallback := configurationFallback{ctx: ctx, launch: func() { opens++ }}
	fallback.beginShow()
	fallback.nativeFailure(errors.New("native view failed"), true, func(error) { quit() })
	if opens != 0 {
		t.Fatal("context shutdown during failure reporting still launched a browser")
	}
}

func TestConfigurationFallbackSerializesConcurrentFailureCallbacks(t *testing.T) {
	var opens atomic.Int32
	fallback := configurationFallback{launch: func() { opens.Add(1) }}
	fallback.beginShow()
	var workers sync.WaitGroup
	for range 20 {
		workers.Go(func() { fallback.open() })
		workers.Go(func() { fallback.nativeFailed(true) })
	}
	workers.Wait()
	if opens.Load() != 1 {
		t.Fatalf("concurrent callbacks opened %d fallbacks", opens.Load())
	}
}
