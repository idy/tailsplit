package desktopwindow

import (
	"errors"
	"reflect"
	"testing"
)

func TestConnectRequestReservationTracksInteractiveNativeLifetime(t *testing.T) {
	w := &Window{done: make(chan struct{})}
	pending := "cold-request"
	var offered []string
	w.SetConnectRequestHandler(func() { offered = append(offered, pending) })
	if !w.CanPresentConnectRequest() {
		t.Fatal("cold live window cannot reserve its first presentation")
	}
	w.offerConnectRequestIfShown()
	if w.CanReceiveConnectRequest() {
		t.Fatal("cold renderer was already eligible to reserve requests")
	}
	if len(offered) != 0 {
		t.Fatal("cold, not-yet-interactive native window took fallback's request")
	}
	// showWindow reserves before native.Show exposes the page to interaction.
	w.offerConnectRequest()
	w.everShown.Store(true)
	if !w.CanReceiveConnectRequest() {
		t.Fatal("presented live page lost request reservation")
	}
	pending = "already-visible-request"
	w.offerConnectRequestIfShown()
	if !reflect.DeepEqual(offered, []string{"cold-request", "already-visible-request"}) {
		t.Fatal("interactive request was not reserved before wakeup", offered)
	}
	w.complete(errors.New("native window failed"))
	if w.CanReceiveConnectRequest() {
		t.Fatal("late state read from failed native page could reserve a fresh request")
	}
	pending = "fresh-browser-request"
	if err := w.Show(); err == nil {
		t.Fatal("failed native window unexpectedly accepted Show")
	}
	if len(offered) != 2 {
		t.Fatal("fresh explicit request after native failure lost browser fallback", offered)
	}
}

func TestColdNativeFailureAndQuitDoNotReserveBrowserRequests(t *testing.T) {
	for _, quitting := range []bool{false, true} {
		w := &Window{done: make(chan struct{})}
		calls := 0
		w.SetConnectRequestHandler(func() { calls++ })
		if quitting {
			w.everShown.Store(true)
			w.quitting.Store(true)
		} else {
			w.complete(errors.New("load failed before presentation"))
		}
		w.offerConnectRequest()
		if w.CanPresentConnectRequest() || w.CanReceiveConnectRequest() {
			t.Fatal("unavailable native page remained eligible to reserve requests")
		}
		if calls != 0 {
			t.Fatal("unavailable native window reserved a fresh fallback request")
		}
	}
}
