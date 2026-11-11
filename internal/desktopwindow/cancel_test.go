package desktopwindow

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/egoist/mygo"
)

func TestNativeCancellationUsesOnlyItsRegisteredMainPage(t *testing.T) {
	frame, other := &mygo.Window{}, &mygo.Window{}
	page, child := &mygo.Page{}, &mygo.Page{}
	bindings := cancellationBindings{windows: make(map[*mygo.Window]cancellationBinding)}
	calls := 0
	window := &Window{cancelHandler: func(ctx context.Context, request, operation string) error {
		calls++
		if ctx.Err() != nil || request != "request" || operation != "operation" {
			t.Fatal("cancellation lost its target or inherited renderer teardown", ctx.Err(), request, operation)
		}
		return nil
	}}
	bindings.register(frame, page, window)
	for _, caller := range []struct {
		frame *mygo.Window
		page  *mygo.Page
	}{{nil, nil}, {other, page}, {frame, child}, {frame, nil}} {
		if err := bindings.cancelFrom(context.Background(), caller.frame, caller.page, "request", "operation"); err == nil || calls != 0 {
			t.Fatal("unregistered page reached native cancellation", err, calls)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Model a native panel closing immediately after IPC delivery.
	if err := bindings.cancelFrom(ctx, frame, page, "request", "operation"); err != nil || calls != 1 {
		t.Fatal("native cancellation was lost after renderer teardown", err, calls)
	}
}

func TestNativeCancellationRejectsMalformedTargetsAndPreservesHostErrors(t *testing.T) {
	frame, page := &mygo.Window{}, &mygo.Page{}
	bindings := cancellationBindings{windows: make(map[*mygo.Window]cancellationBinding)}
	window := &Window{}
	bindings.register(frame, page, window)
	if err := bindings.cancelFrom(context.Background(), frame, page, "request", ""); !errors.Is(err, ErrNotReady) {
		t.Fatal("missing guarded host fell back", err)
	}
	failure := errors.New("cleanup still in progress")
	calls := 0
	window.cancelHandler = func(context.Context, string, string) error { calls++; return failure }
	for _, target := range [][2]string{{"", ""}, {strings.Repeat("x", 1025), ""}, {"id", strings.Repeat("y", 1024)}} {
		if err := bindings.cancelFrom(context.Background(), frame, page, target[0], target[1]); err == nil || calls != 0 {
			t.Fatal("malformed native request reached host", err, calls)
		}
	}
	if err := bindings.cancelFrom(context.Background(), frame, page, "request", ""); !errors.Is(err, failure) || calls != 1 {
		t.Fatal("native cancellation hid its host result", err, calls)
	}
}
