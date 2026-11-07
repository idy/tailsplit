package main

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/idy/tailsplit"
)

func TestExitPingPreviewIsExplicitlySimulatedAndDoesNotChangeConnection(t *testing.T) {
	options := testDesktopPreview(t)
	if !options.Preview || options.PingExit == nil || options.Snapshot().PingAvailable {
		t.Fatal("preview advertised real or signed-out ping")
	}
	if _, err := options.PingExit(context.Background(), "100.64.0.10"); !errors.Is(err, tailsplit.ErrPingUnavailable) {
		t.Fatal(err)
	}
	previewAction(t, options, "login")
	before := options.Snapshot()
	data, err := os.ReadFile(options.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		id     string
		ms     float64
		direct bool
	}{{"100.64.0.10", 23.4, true}, {"100.64.0.20", 740, false}, {"100.64.0.40", 1250, false}} {
		result, err := options.PingExit(context.Background(), test.id)
		if err != nil || result.ID != test.id || result.LatencyMS != test.ms || result.Direct != test.direct {
			t.Fatalf("simulation mismatch: %+v %v", result, err)
		}
		if !result.Direct && result.Relay != "preview-simulated" {
			t.Fatal("relay simulation lacks preview label")
		}
	}
	if _, err := options.PingExit(context.Background(), "100.64.0.30"); !errors.Is(err, tailsplit.ErrPingOffline) {
		t.Fatal("offline preview node accepted", err)
	}
	if _, err := options.PingExit(context.Background(), "203.0.113.1"); !errors.Is(err, tailsplit.ErrPingTarget) {
		t.Fatal("arbitrary preview target accepted", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := options.PingExit(ctx, "100.64.0.10"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(options.ConfigPath)
	if !reflect.DeepEqual(before, options.Snapshot()) || string(data) != string(after) {
		t.Fatal("preview ping changed saved policy or connection state")
	}
	previewAction(t, options, "logout")
	if options.Snapshot().PingAvailable {
		t.Fatal("logout retained ping capability")
	}
}
