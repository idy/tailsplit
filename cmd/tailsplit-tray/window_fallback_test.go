package main

import (
	"sync"
	"sync/atomic"
	"testing"
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
