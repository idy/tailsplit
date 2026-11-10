package desktopwindow

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestTrayPopupURL(t *testing.T) {
	const valid = "http://127.0.0.1:43821/tray.html#private-capability"
	popup, err := NewTrayPopup(valid)
	if err != nil || popup.port != 43821 || popup.url != valid {
		t.Fatalf("valid popup URL rejected: %v", err)
	}
	for _, raw := range []string{
		"", "https://127.0.0.1:43821/tray.html#private-capability",
		"http://localhost:43821/tray.html#private-capability",
		"http://127.0.0.1:43821@evil.example/tray.html#private-capability",
		"http://127.0.0.1:43821.evil.example/tray.html#private-capability",
		"http://127.0.0.1:43821/tray.html?token=private-capability",
		"http://127.0.0.1:43821/tray.html?x=1#private-capability",
		"http://127.0.0.1:43821/#private-capability",
		"http://127.0.0.1:43821/tray.html",
		"http://127.0.0.1:43821/tray.html#private-capability\\x",
		"http://127.0.0.1:43821/tray.html#%zz-private-capability",
	} {
		popup, err := NewTrayPopup(raw)
		if popup != nil || err == nil {
			t.Errorf("accepted invalid URL %q", raw)
		} else if strings.Contains(err.Error(), "private-capability") {
			t.Error("URL validation leaked capability")
		}
	}
}

func TestTrayPopupReadinessRequiresUsablePage(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	calls := 0
	err := probeTrayPopupReadiness(ctx, func(_ context.Context, script string) (any, error) {
		for _, required := range []string{"document.readyState === 'complete'", "location.origin ===", "http://127.0.0.1:43821", "location.pathname === '/tray.html'", "dataset.trayReady === 'true'"} {
			if !strings.Contains(script, required) {
				t.Fatalf("readiness omitted %q", required)
			}
		}
		calls++
		return calls > 1, nil
	}, "http://127.0.0.1:43821", time.Millisecond)
	if err != nil || calls != 2 {
		t.Fatalf("readiness = %v after %d calls", err, calls)
	}
}

func testPopupAttempt(t *testing.T) *popupAttempt {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &popupAttempt{load: &pageLoad{ctx: ctx, cancel: cancel}}
}

func TestTrayPopupCloseInvalidatesQueuedReadiness(t *testing.T) {
	a := testPopupAttempt(t)
	p := &TrayPopup{attempt: a}
	if !p.pending(a) {
		t.Fatal("new attempt is not pending")
	}
	p.Close()
	if p.attempt != nil || !a.closed || a.load.ctx.Err() != context.Canceled || p.pending(a) {
		t.Fatal("closing a loading popup left a valid readiness callback")
	}
	a.fallback = func() { t.Fatal("a dismissed attempt invoked fallback") }
	p.fail(a)

	fresh := testPopupAttempt(t)
	p.attempt = fresh
	p.closed(a) // Delayed close callback from a previous window.
	if p.attempt != fresh || !p.pending(fresh) || p.pending(a) {
		t.Fatal("old close callback disturbed the new popup")
	}
}

func TestTrayPopupFailureFallsBackOnce(t *testing.T) {
	for _, state := range []string{"loading", "timeout", "loaded"} {
		t.Run(state, func(t *testing.T) {
			a := testPopupAttempt(t)
			if state == "timeout" {
				ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				t.Cleanup(cancel)
				a.load = &pageLoad{ctx: ctx, cancel: cancel}
			}
			p := &TrayPopup{attempt: a}
			if state == "loaded" {
				a.loaded = true
				a.load.cancel() // The successful readiness probe is no longer needed.
			} else if !p.pending(a) {
				t.Fatal("deadline completion must still be handled")
			}
			calls := 0
			a.fallback = func() {
				calls++
				if p.attempt != nil || !a.closed || a.load.ctx.Err() == nil {
					t.Fatal("fallback ran before disposing the failed attempt")
				}
			}
			p.fail(a)
			p.fail(a)
			if calls != 1 {
				t.Fatalf("fallback invoked %d times", calls)
			}
		})
	}
}

func TestTrayPopupFailureIgnoresCanceledOrReplacedAttempt(t *testing.T) {
	for _, state := range []string{"canceled", "replaced"} {
		t.Run(state, func(t *testing.T) {
			a := testPopupAttempt(t)
			p := &TrayPopup{attempt: a}
			a.fallback = func() { t.Fatal("obsolete callback invoked fallback") }
			if state == "canceled" {
				a.load.cancel()
			} else {
				p.attempt = testPopupAttempt(t)
			}
			p.fail(a)
			if p.pending(a) {
				t.Fatal("obsolete readiness callback remains valid")
			}
		})
	}
}

func TestTrayPopupReadinessObservation(t *testing.T) {
	a := testPopupAttempt(t)
	value, err := a.observeReadiness(context.Background(), "false", func(context.Context, string) (any, error) {
		if !a.probing.Load() {
			t.Fatal("in-flight evaluation was not observable")
		}
		return map[string]any{"ready": false, "complete": true, "marker": false, "hidden": true, "body": true, "i18n": true}, nil
	})
	got := a.observed.Load()
	if value != false || err != nil || a.probing.Load() || !got.Complete || got.Marker || !got.Hidden || !got.Body || !got.I18n || got.Failed {
		t.Fatalf("incorrect safe page observation: %+v", got)
	}
	_, _ = a.observeReadiness(context.Background(), "false", func(context.Context, string) (any, error) {
		return nil, errors.New("private-capability")
	})
	if !a.observed.Load().Failed || a.probing.Load() {
		t.Fatal("failed evaluation was not recorded")
	}
}

func TestTrayPopupReadinessTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := probeTrayPopupReadiness(ctx, func(ctx context.Context, _ string) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}, "http://127.0.0.1:43821", time.Millisecond)
	if !errors.Is(err, errPanelLoadTimeout) {
		t.Fatalf("unresponsive WebView did not time out: %v", err)
	}
}
