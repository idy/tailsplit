package desktopwindow

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestProbePageReadiness(t *testing.T) {
	const origin = "http://127.0.0.1:43821"
	const capabilityURL = origin + "/#private-panel-capability"
	t.Run("ready after navigation", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		calls := 0
		err := probePageReadiness(ctx, func(_ context.Context, script string) (any, error) {
			if !strings.Contains(script, origin) || strings.Contains(script, "private-panel-capability") {
				t.Fatalf("probe must use only the panel origin: %q", script)
			}
			calls++
			if calls == 1 {
				// A navigation may invalidate an evaluation's execution context.
				return nil, errors.New("execution context destroyed: " + capabilityURL)
			}
			return calls > 2, nil
		}, origin, time.Millisecond)
		if err != nil || calls != 3 {
			t.Fatalf("readiness = %v after %d calls", err, calls)
		}
	})
	t.Run("asynchronous runtime failure", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		err := probePageReadiness(ctx, func(context.Context, string) (any, error) {
			return nil, errors.New("WebView2 environment/controller failed: " + capabilityURL)
		}, origin, time.Millisecond)
		if !errors.Is(err, errWebViewReadiness) || strings.Contains(err.Error(), capabilityURL) || strings.Contains(err.Error(), "private-panel-capability") {
			t.Fatalf("unsafe or missing runtime failure: %v", err)
		}
	})
	t.Run("evaluation never answers", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		err := probePageReadiness(ctx, func(ctx context.Context, _ string) (any, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}, origin, time.Millisecond)
		if !errors.Is(err, errPanelLoadTimeout) {
			t.Fatalf("missing bounded timeout: %v", err)
		}
	})
	t.Run("page never becomes ready", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		err := probePageReadiness(ctx, func(context.Context, string) (any, error) {
			return false, nil
		}, origin, time.Millisecond)
		if !errors.Is(err, errPanelLoadTimeout) {
			t.Fatalf("missing page readiness timeout: %v", err)
		}
	})
	t.Run("canceled before evaluation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := probePageReadiness(ctx, func(context.Context, string) (any, error) {
			t.Fatal("canceled probe evaluated the page")
			return nil, nil
		}, origin, time.Millisecond)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation = %v", err)
		}
	})
	t.Run("canceled during evaluation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		err := probePageReadiness(ctx, func(ctx context.Context, _ string) (any, error) {
			cancel()
			<-ctx.Done()
			return nil, ctx.Err()
		}, origin, time.Millisecond)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation = %v", err)
		}
	})
}

func TestLoadFailureCompletesBeforeDestroy(t *testing.T) {
	w := &Window{done: make(chan struct{})}
	load := newPageLoad()
	defer load.cancel()
	w.load = load
	destroyed := false
	w.failLoad(load, false, errWebViewReadiness, func() {
		destroyed = true
		if !w.isDone() || !errors.Is(w.result(), errWebViewReadiness) {
			t.Fatal("host destroyed before publishing the failure")
		}
		if !errors.Is(load.ctx.Err(), context.Canceled) {
			t.Fatal("failure left the probe active")
		}
		// Model MyGo's synchronous OnClosed callback.
		w.complete(nil)
	})
	if !destroyed || !errors.Is(w.Run(), errWebViewReadiness) {
		t.Fatal("Run lost the startup failure")
	}
	if !errors.Is(w.Show(), errWebViewReadiness) {
		t.Fatal("Show did not report the completed startup failure")
	}
}

func TestLateLoadFailureIgnored(t *testing.T) {
	for _, state := range []string{"loaded", "quitting", "destroyed", "replaced", "stopped", "completed", "canceled"} {
		t.Run(state, func(t *testing.T) {
			w := &Window{done: make(chan struct{})}
			load := newPageLoad()
			defer load.cancel()
			w.load = load
			destroyed := false
			err := errWebViewReadiness
			switch state {
			case "loaded":
				w.loaded = true
			case "quitting":
				w.quitting.Store(true)
			case "destroyed":
				destroyed = true
			case "replaced":
				w.load = newPageLoad()
				defer w.load.cancel()
			case "stopped":
				w.stopLoadProbe()
			case "completed":
				w.complete(nil)
			case "canceled":
				load.cancel()
				err = context.Canceled
			}
			w.failLoad(load, destroyed, err, func() { t.Fatal("late probe destroyed the host") })
			if w.result() != nil || state != "completed" && w.isDone() {
				t.Fatal("late probe completed the window")
			}
		})
	}
}

func TestWindowCreationOnRun(t *testing.T) {
	for _, test := range []struct {
		platform string
		visible  bool
		proof    bool
		want     bool
	}{
		{"darwin", false, false, false},
		{"darwin", true, false, true},
		{"darwin", false, true, true},
		{"windows", false, false, true},
		{"windows", true, false, true},
		{"windows", false, true, true},
	} {
		if got := createWindowOnRun(test.platform, test.visible, test.proof); got != test.want {
			t.Errorf("creation on %s (visible %t, proof %t) = %t; want %t", test.platform, test.visible, test.proof, got, test.want)
		}
	}
}

func TestBackgroundMacRunWaitsWithoutSchedulingWindow(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS background lifecycle")
	}
	t.Setenv("TAILSPLIT_DESKTOP_PROOF_DIR", "")
	w := &Window{done: make(chan struct{})}
	defer w.complete(nil)
	result := make(chan error, 1)
	go func() { result <- w.Run() }()
	select {
	case err := <-result:
		t.Fatalf("background Run returned before quit: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	w.complete(nil)
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("background Run did not finish on quit")
	}
	unused := false
	w.start.Do(func() { unused = true })
	if !unused {
		t.Fatal("background Run scheduled native window creation")
	}
}
