package desktopwindow

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
)

const nativeCancelPath = "/native-cancel"
const nativeCancelArmPath = "/native-cancel-arm"
const nativeCancelAttemptTimeout = 15 * time.Second
const nativeCancelAckTimeout = 2 * time.Second

type nativeCancelHandler func(context.Context, string, string) error

// This queue belongs to the host, not to any renderer or website data store.
// Only its delivery worker calls the guarded handler; journal reads never gate it.
type nativeCancellationInbox struct {
	ctx          context.Context
	handler      func() nativeCancelHandler
	mu           sync.Mutex
	pending      map[journalCancellation]bool
	armed        map[journalCancellation]bool // Promoted only on actual source loss, never normal hide.
	acknowledged map[journalCancellation]bool
	job          *journalRecovery
	running      bool
	wake         chan struct{}
	ack          func(journalCancellation) // Callbacks must not wait for renderer Eval.
	armedAck     func(journalCancellation)
	admittedAck  func(journalCancellation)
}

func newNativeCancellationInbox(ctx context.Context, handler func() nativeCancelHandler) *nativeCancellationInbox {
	return &nativeCancellationInbox{ctx: ctx, handler: handler, pending: make(map[journalCancellation]bool), armed: make(map[journalCancellation]bool), acknowledged: make(map[journalCancellation]bool), wake: make(chan struct{}, 1)}
}

func (i *nativeCancellationInbox) retry() {
	select {
	case i.wake <- struct{}{}:
	default:
	}
}

// arm stores the exact fail-safe target before acknowledging it. No worker is
// started and no current owner is stopped while the original source survives.
func (i *nativeCancellationInbox) arm(record journalCancellation) bool {
	i.mu.Lock()
	if i.ctx.Err() != nil {
		i.mu.Unlock()
		return false
	}
	completed := i.acknowledged[record]
	if !completed && !i.pending[record] {
		i.armed[record] = true
	}
	armedAck, ack := i.armedAck, i.ack
	i.mu.Unlock()
	if armedAck != nil {
		armedAck(record)
	}
	if completed && ack != nil {
		ack(record)
	}
	return true
}

func (i *nativeCancellationInbox) enqueueLocked(record journalCancellation) bool {
	delete(i.armed, record)
	if i.acknowledged[record] {
		return true
	}
	i.pending[record] = true
	if i.job == nil {
		i.job = &journalRecovery{done: make(chan struct{})}
	}
	if !i.running {
		i.running = true
		go i.deliver()
	} else {
		i.retry()
	}
	return false
}

func (i *nativeCancellationInbox) enqueue(record journalCancellation) bool {
	i.mu.Lock()
	if i.ctx.Err() != nil {
		i.mu.Unlock()
		return false
	}
	completed := i.enqueueLocked(record)
	admittedAck, ack := i.admittedAck, i.ack
	i.mu.Unlock()
	if admittedAck != nil {
		admittedAck(record)
	}
	if completed && ack != nil {
		ack(record)
	}
	return true
}

func (i *nativeCancellationInbox) promoteArmed() {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.ctx.Err() != nil {
		return
	}
	for record := range i.armed {
		i.enqueueLocked(record)
	}
}

func (i *nativeCancellationInbox) pendingRecovery() *journalRecovery {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.job
}

func (i *nativeCancellationInbox) finishLocked(err error) {
	if i.job != nil {
		i.job.err = err
		close(i.job.done)
		i.job = nil
	}
	i.running = false
}

func (i *nativeCancellationInbox) deliver() {
	delay := cancelJournalRetry
	for {
		i.mu.Lock()
		if err := i.ctx.Err(); err != nil {
			i.finishLocked(err)
			i.mu.Unlock()
			return
		}
		if len(i.pending) == 0 {
			i.finishLocked(nil)
			i.mu.Unlock()
			return
		}
		var targets []journalCancellation
		for target := range i.pending {
			targets = append(targets, target)
		}
		i.mu.Unlock()
		sort.Slice(targets, func(a, b int) bool {
			if targets[a].Request != targets[b].Request {
				return targets[a].Request < targets[b].Request
			}
			return targets[a].OperationID < targets[b].OperationID
		})
		for _, target := range targets {
			if i.ctx.Err() != nil {
				break
			}
			handler := i.handler()
			if handler == nil {
				continue
			}
			ctx, stop := context.WithTimeout(i.ctx, nativeCancelAttemptTimeout)
			err := handler(ctx, target.Request, target.OperationID)
			stop()
			if err != nil {
				continue
			}
			i.mu.Lock()
			delete(i.pending, target)
			i.acknowledged[target] = true
			ack := i.ack
			i.mu.Unlock()
			if ack != nil {
				ack(target)
			}
		}
		i.mu.Lock()
		if len(i.pending) == 0 {
			i.finishLocked(nil)
			i.mu.Unlock()
			return
		}
		i.mu.Unlock()
		timer := time.NewTimer(delay)
		select {
		case <-i.ctx.Done():
		case <-i.wake:
		case <-timer.C:
		}
		timer.Stop()
		if delay < 2*time.Second {
			delay *= 2
		}
	}
}

func sameCancellationOrigin(candidate *url.URL, origin string) bool {
	base, err := url.Parse(origin)
	if err != nil || candidate.User != nil || candidate.Opaque != "" || candidate.Scheme != base.Scheme {
		return false
	}
	if candidate.Host == base.Host {
		return true
	}
	// WebKit omits the default HTTP port in its navigation URLs.
	return base.Scheme == "http" && base.Port() == "80" && candidate.Host == base.Hostname()
}

func parseNativeCancellation(origin, scope, raw string) (journalCancellation, bool) {
	return parseNativeCancellationPath(origin, scope, raw, nativeCancelPath)
}

func parseNativeCancellationPath(origin, scope, raw, path string) (journalCancellation, bool) {
	var empty journalCancellation
	if path != nativeCancelPath && path != nativeCancelArmPath {
		return empty, false
	}
	if scope == "" || len(raw) > 8192 {
		return empty, false
	}
	parsed, err := url.Parse(raw)
	if err != nil || !sameCancellationOrigin(parsed, origin) || parsed.Path != path || parsed.EscapedPath() != path || parsed.RawQuery != "" || parsed.ForceQuery {
		return empty, false
	}
	// Parse the escaped fragment once. Parsing URL.Fragment would decode escaped
	// '&' or '=' twice and could change the exact target fields.
	fields, err := url.ParseQuery(parsed.EscapedFragment())
	if err != nil || len(fields) != 3 {
		return empty, false
	}
	for _, name := range []string{"scope", "request", "operation_id"} {
		if len(fields[name]) != 1 {
			return empty, false
		}
	}
	record := journalCancellation{Scope: fields.Get("scope"), Request: fields.Get("request"), OperationID: fields.Get("operation_id")}
	if record.Scope != scope || record.Request == "" && record.OperationID == "" || len(record.Request)+len(record.OperationID) > 1024 {
		return empty, false
	}
	return record, true
}

func nativeCancellationPath(raw string) string {
	head, _, _ := strings.Cut(raw, "#")
	parsed, err := url.Parse(head)
	if err == nil && (parsed.Path == nativeCancelPath || parsed.Path == nativeCancelArmPath) {
		return parsed.Path
	}
	return ""
}

func (w *Window) nativeCancellationInbox() *nativeCancellationInbox {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.navigationInbox == nil {
		ctx := w.journalContext
		if ctx == nil {
			ctx = context.Background()
		}
		w.navigationInbox = newNativeCancellationInbox(ctx, func() nativeCancelHandler {
			w.mu.Lock()
			defer w.mu.Unlock()
			return w.cancelHandler
		})
	}
	return w.navigationInbox
}

func (w *Window) promoteArmedCancellations() {
	w.mu.Lock()
	inbox := w.navigationInbox
	w.mu.Unlock()
	if inbox != nil {
		inbox.promoteArmed()
	}
}

func (w *Window) pendingNativeCancellation() *journalRecovery {
	w.mu.Lock()
	inbox := w.navigationInbox
	w.mu.Unlock()
	if inbox == nil {
		return nil
	}
	return inbox.pendingRecovery()
}

// SDK WillNavigate emits only requested top-level navigations (not subframe,
// reload or back/forward events). Register only the bound main Page, before the
// ordinary navigation policy. Keeper pages have neither this hook nor binding.
func (b *cancellationBindings) cancellationNavigation(w *Window, frame *mygo.Window, page *mygo.Page, origin, sourceURL string, destroyed bool, event *mygo.NavigateEvent) bool {
	path := nativeCancellationPath(event.URL)
	if path == "" {
		return false
	}
	event.PreventDefault()
	b.mu.Lock()
	binding, known := b.windows[frame]
	b.mu.Unlock()
	if destroyed || w.rendererUnavailable.Load() || w.quitting.Load() || w.isDone() || !known || binding.window != w || page == nil || binding.page != page {
		return false
	}
	source, err := url.Parse(sourceURL)
	if err != nil || !sameCancellationOrigin(source, origin) || source.Path != "/" || source.RawQuery != "" {
		return false
	}
	record, valid := parseNativeCancellationPath(origin, w.journalScope, event.URL, path)
	if !valid {
		return false
	}
	inbox := w.nativeCancellationInbox()
	if path == nativeCancelArmPath {
		return inbox.arm(record)
	}
	return inbox.enqueue(record)
}

func nativeCancellationEventCode(origin, event string, record journalCancellation) string {
	data, _ := json.Marshal(record)
	return `(() => { if (location.origin === ` + strconv.Quote(origin) + ` && location.pathname === '/') return window.dispatchEvent(new CustomEvent(` + strconv.Quote(event) + `,{detail:` + string(data) + `})); return false; })()`
}

func (w *Window) registerCancellationNavigation(frame *mygo.Window, page *mygo.Page, origin string) {
	inbox := w.nativeCancellationInbox()
	emit := func(event string) func(journalCancellation) {
		return func(record journalCancellation) {
			go func() {
				if frame.IsDestroyed() {
					return
				}
				ctx, stop := context.WithTimeout(inbox.ctx, nativeCancelAckTimeout)
				defer stop()
				_, _ = page.EvalContext(ctx, nativeCancellationEventCode(origin, event, record))
			}()
		}
	}
	inbox.mu.Lock()
	inbox.ack = emit("tailsplit-native-cancel-ack")
	inbox.armedAck = emit("tailsplit-native-cancel-armed")
	inbox.admittedAck = emit("tailsplit-native-cancel-admitted")
	inbox.mu.Unlock()
	page.OnWillNavigate(func(event *mygo.NavigateEvent) {
		panelCancellation.cancellationNavigation(w, frame, page, origin, page.URL(), frame.IsDestroyed(), event)
	})
}
