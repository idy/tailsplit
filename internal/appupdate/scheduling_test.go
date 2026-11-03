package appupdate

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestConcurrentCheckAndDownloadStayResponsive(t *testing.T) {
	f := fixture(t, "v0.6.2", "darwin", "arm64")
	checkStarted, downloadStarted := make(chan struct{}), make(chan struct{})
	allowCheck, allowDownload := make(chan struct{}), make(chan struct{})
	var releaseCheck, releaseDownload sync.Once
	defer releaseCheck.Do(func() { close(allowCheck) })
	defer releaseDownload.Do(func() { close(allowDownload) })
	u, log := testUpdater(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/latest") {
			close(checkStarted)
			select {
			case <-allowCheck:
			case <-r.Context().Done():
				return
			}
		}
		if strings.HasSuffix(r.URL.Path, ".zip") {
			_, _ = w.Write(f.payload[:1])
			w.(http.Flusher).Flush()
			close(downloadStarted)
			select {
			case <-allowDownload:
				_, _ = w.Write(f.payload[1:])
			case <-r.Context().Done():
			}
			return
		}
		f.serve(w, r)
	})
	checked := make(chan error, 1)
	go func() { checked <- u.Check(context.Background()) }()
	waitSignal(t, checkStarted)
	state := responsiveSnapshot(t, u)
	if !state.Checking || state.Downloading {
		t.Fatalf("incorrect active check flags: %+v", state)
	}
	if err := u.Check(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatalf("concurrent check was not rejected: %v", err)
	}
	if _, err := u.Download(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatalf("download during check was not rejected: %v", err)
	}
	u.SetAutomatic(false)
	if responsiveSnapshot(t, u).Automatic {
		t.Fatal("preference change blocked or was ignored during check")
	}
	releaseCheck.Do(func() { close(allowCheck) })
	if err := waitError(t, checked); err != nil {
		t.Fatal(err)
	}
	downloaded := make(chan error, 1)
	go func() {
		pkg, err := u.Download(context.Background())
		if pkg.Path != "" {
			_ = pkg.Cleanup()
		}
		downloaded <- err
	}()
	waitSignal(t, downloadStarted)
	state = responsiveSnapshot(t, u)
	if state.Checking || !state.Downloading || state.TotalBytes != int64(len(f.payload)) {
		t.Fatalf("incorrect active download flags: %+v", state)
	}
	// The first flushed byte must become observable before the transfer ends.
	deadline := time.After(2 * time.Second)
	ticks := time.NewTicker(time.Millisecond)
	defer ticks.Stop()
	for u.Snapshot().DownloadedBytes == 0 {
		select {
		case <-ticks.C:
		case <-deadline:
			t.Fatal("download progress remained zero during transfer")
		}
	}
	if err := u.Check(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatalf("check during download was not rejected: %v", err)
	}
	if _, err := u.Download(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatalf("concurrent download was not rejected: %v", err)
	}
	u.SetAutomatic(true)
	if !responsiveSnapshot(t, u).Automatic {
		t.Fatal("preference change was ignored during download")
	}
	releaseDownload.Do(func() { close(allowDownload) })
	if err := waitError(t, downloaded); err != nil {
		t.Fatal(err)
	}
	if log.count() != 3 {
		t.Fatalf("concurrent operations made extra requests: %d", log.count())
	}
	requireEmptyTemp(t, u)
}

func responsiveSnapshot(t *testing.T, u *Updater) State {
	t.Helper()
	result := make(chan State, 1)
	go func() { result <- u.Snapshot() }()
	select {
	case state := <-result:
		return state
	case <-time.After(time.Second):
		t.Fatal("snapshot blocked on network operation")
		return State{}
	}
}

type timerRequest struct {
	delay time.Duration
	tick  chan time.Time
}

func fakeTimers(u *Updater) <-chan timerRequest {
	queue := make(chan timerRequest, 8)
	u.after = func(delay time.Duration) <-chan time.Time {
		tick := make(chan time.Time, 1)
		queue <- timerRequest{delay: delay, tick: tick}
		return tick
	}
	return queue
}

func nextTimer(t *testing.T, queue <-chan timerRequest, delay time.Duration) timerRequest {
	t.Helper()
	select {
	case timer := <-queue:
		if timer.delay != delay {
			t.Fatalf("incorrect scheduled delay: got %s, want %s", timer.delay, delay)
		}
		return timer
	case <-time.After(time.Second):
		t.Fatal("scheduler did not request its next timer")
		return timerRequest{}
	}
}

func TestRunPreferenceSchedulingAndManualCheck(t *testing.T) {
	f := fixture(t, "v0.6.2", "darwin", "arm64")
	u, log := testUpdater(t, f.serve)
	u.SetAutomatic(false)
	timers := fakeTimers(u)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan struct{})
	go func() { u.Run(ctx); close(finished) }()
	startup := nextTimer(t, timers, 10*time.Second)
	if log.count() != 0 {
		t.Fatal("scheduler checked before startup delay")
	}
	startup.tick <- time.Now()
	interval := nextTimer(t, timers, 6*time.Hour)
	if log.count() != 0 || !u.Snapshot().LastChecked.IsZero() {
		t.Fatal("disabled automatic check was not skipped")
	}
	u.SetAutomatic(true)
	interval.tick <- time.Now()
	interval = nextTimer(t, timers, 6*time.Hour)
	if log.count() != 1 || !u.Snapshot().Available {
		t.Fatal("enabled periodic check did not discover update")
	}
	u.SetAutomatic(false)
	interval.tick <- time.Now()
	nextTimer(t, timers, 6*time.Hour)
	if log.count() != 1 {
		t.Fatal("disabled periodic check made a request")
	}
	requireCheck(t, u)
	if log.count() != 2 || u.Snapshot().Automatic {
		t.Fatal("manual check did not respect its independent preference semantics")
	}
	cancel()
	waitSignal(t, finished)
}

func TestRunChecksAfterStartupAndSkipsDevelopment(t *testing.T) {
	for _, version := range []string{"0.6.1", "dev", "0.6.2-dev"} {
		t.Run(version, func(t *testing.T) {
			f := fixture(t, "v0.6.2", "darwin", "arm64")
			u, log := testUpdater(t, f.serve)
			u.current, _ = stableVersion(version)
			u.state.CurrentVersion = version
			timers := fakeTimers(u)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			finished := make(chan struct{})
			go func() { u.Run(ctx); close(finished) }()
			startup := nextTimer(t, timers, 10*time.Second)
			startup.tick <- time.Now()
			interval := nextTimer(t, timers, 6*time.Hour)
			interval.tick <- time.Now()
			nextTimer(t, timers, 6*time.Hour)
			wanted := 0
			if version == "0.6.1" {
				wanted = 2
			}
			if log.count() != wanted {
				t.Fatalf("incorrect automatic checks for %s: %d", version, log.count())
			}
			cancel()
			waitSignal(t, finished)
		})
	}
}

func TestOnlyOneRunLoopAndCanceledLifecycle(t *testing.T) {
	u := New("0.6.1", false)
	timers := fakeTimers(u)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan struct{})
	go func() { u.Run(ctx); close(finished) }()
	nextTimer(t, timers, 10*time.Second)
	second := make(chan struct{})
	go func() { u.Run(ctx); close(second) }()
	waitSignal(t, second)
	cancel()
	waitSignal(t, finished)
	u.Run(ctx)
	select {
	case <-timers:
		t.Fatal("canceled lifecycle scheduled another timer")
	default:
	}
}
