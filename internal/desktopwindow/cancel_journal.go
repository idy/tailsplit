package desktopwindow

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
)

const cancelJournalPrefix = "tailsplit-native-cancel-"
const cancelJournalGrace = 250 * time.Millisecond
const cancelJournalRetry = 250 * time.Millisecond

type journalCancellation struct {
	Scope       string `json:"scope"`
	Request     string `json:"request"`
	OperationID string `json:"operation_id"`
}

type journalRecovery struct {
	done chan struct{}
	err  error // Published by closing done.
}

// Both stores belong to the native session rather than the failed renderer.
// Backup Eval runs only in the separate untrusted keeper. No panel capability,
// login URL or device credentials enter this journal.
type cancellationJournal struct {
	scope  string
	list   func() ([]mygo.Cookie, error)
	remove func(mygo.Cookie) error
	backup *journalStorage

	mu         sync.Mutex
	generation uint64
	pending    *journalRecovery
}

func cancellationJournalURL(raw string) (string, string) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw, ""
	}
	fragment, err := url.ParseQuery(parsed.Fragment)
	if err != nil {
		return raw, ""
	}
	scope := rand.Text()
	fragment.Set("journal", scope)
	parsed.Fragment, parsed.RawFragment = fragment.Encode(), ""
	return parsed.String(), scope
}

func newCancellationJournal(session *mygo.Session, scope, origin string) *cancellationJournal {
	return &cancellationJournal{
		scope: scope,
		list: func() ([]mygo.Cookie, error) {
			return session.Cookies.List(mygo.CookieFilter{URL: origin + "/"})
		},
		remove: session.Cookies.Delete,
	}
}

func (j *cancellationJournal) decode(cookie mygo.Cookie) (journalCancellation, bool) {
	var record journalCancellation
	if j.scope == "" || !strings.HasPrefix(cookie.Name, cancelJournalPrefix+j.scope+"-") || len(cookie.Value) > 8192 {
		return record, false
	}
	data, err := url.PathUnescape(cookie.Value)
	if err != nil || len(data) > 4096 {
		return record, false
	}
	decoder := json.NewDecoder(strings.NewReader(data))
	decoder.DisallowUnknownFields()
	var extra any
	if decoder.Decode(&record) != nil || decoder.Decode(&extra) != io.EOF || record.Scope != j.scope ||
		record.Request == "" && record.OperationID == "" || len(record.Request)+len(record.OperationID) > 1024 {
		return journalCancellation{}, false
	}
	return record, true
}

func (j *cancellationJournal) replay(cancel func(context.Context, string, string) error, ctx context.Context) error {
	_, err := j.replayBatch(cancel, ctx, make(map[string]string))
	return err
}

func (j *cancellationJournal) replayBatch(cancel func(context.Context, string, string) error, ctx context.Context, acknowledged map[string]string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	processed := false
	var failures []error
	attempted := make(map[string]bool)
	deliver := func(cookie mygo.Cookie) bool {
		record, valid := j.decode(cookie)
		if !valid {
			return false
		}
		if err := ctx.Err(); err != nil {
			failures = append(failures, err)
			return false
		}
		// The same decoded record can appear under the same or distinct entry
		// names in both stores. Delivery belongs to the logical target, while
		// deletion still belongs to each exact source name and value.
		data, _ := json.Marshal(record)
		key := "\x00delivered:" + string(data)
		if acknowledged[key] != "yes" {
			if attempted[key] {
				return false
			}
			attempted[key] = true
			if cancel == nil {
				failures = append(failures, ErrNotReady)
				return false
			}
			if err := cancel(ctx, record.Request, record.OperationID); err != nil {
				failures = append(failures, err)
				return false
			}
			processed = true
			acknowledged[key] = "yes"
		}
		acknowledged[cookie.Name] = cookie.Value
		return true
	}
	// Process the independent backup first. Even a cookie SDK call that stalls
	// cannot delay a cancellation already obtained from the keeper reader.
	if j.backup != nil {
		entries, err := j.backup.list(ctx)
		if err != nil {
			failures = append(failures, err)
		}
		for _, entry := range entries {
			if deliver(mygo.Cookie{Name: entry.Name, Value: entry.Value}) && j.backup.remove != nil {
				err := j.backup.remove(ctx, entry)
				if errors.Is(err, errJournalEntryChanged) {
					processed = true // A newer value must be read before releasing the session.
				} else if err != nil {
					failures = append(failures, err)
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return processed, errors.Join(append(failures, err)...)
	}
	cookies, err := j.list()
	if err != nil {
		failures = append(failures, err)
	}
	for _, cookie := range cookies {
		if deliver(cookie) && j.remove != nil {
			// An acknowledged cookie may remain if native deletion fails. The
			// shared delivery ledger prevents replay forever in that case.
			_ = j.remove(cookie)
		}
	}
	return processed, errors.Join(failures...)
}

func (j *cancellationJournal) recover(ctx context.Context, cancel func(context.Context, string, string) error) *journalRecovery {
	j.mu.Lock()
	j.generation++
	if j.pending != nil {
		job := j.pending
		j.mu.Unlock()
		return job
	}
	job := &journalRecovery{done: make(chan struct{})}
	j.pending = job
	j.mu.Unlock()
	go func() {
		retry := cancelJournalRetry
		acknowledged := make(map[string]string)
		for {
			j.mu.Lock()
			generation := j.generation
			j.mu.Unlock()
			err := ctx.Err()
			processed := false
			if err == nil {
				processed, err = j.replayBatch(cancel, ctx, acknowledged)
			}
			j.mu.Lock()
			// A failure arriving during an earlier read requires another read
			// after that failure, so its last committed cookie cannot be missed.
			if ctx.Err() != nil || err == nil && !processed && generation == j.generation {
				job.err = ctx.Err()
				j.pending = nil
				close(job.done)
				j.mu.Unlock()
				return
			}
			j.mu.Unlock()
			if err == nil {
				continue
			}
			timer := time.NewTimer(retry)
			if retry < 2*time.Second {
				retry *= 2
			}
			select {
			case <-timer.C:
			case <-ctx.Done():
			}
			timer.Stop()
		}
	}()
	return job
}

func (w *Window) beginCancellationRecovery() *journalRecovery {
	if w.journal == nil {
		return nil
	}
	ctx := w.journalContext
	if ctx == nil {
		ctx = context.Background()
	}
	w.mu.Lock()
	handler := w.cancelHandler
	w.mu.Unlock()
	return w.journal.recover(ctx, handler)
}

func (w *Window) dispatchRecovery(fn func()) {
	if w.recoveryDispatch != nil {
		w.recoveryDispatch(fn)
	} else {
		mygo.RunOnMain(fn)
	}
}

// Recovery never waits on the UI thread. A slow or failed cookie-store call
// retains the hidden native page/session and one recovery worker, rather than
// destroying the last ephemeral page and losing its journal.
func (w *Window) afterCancellationRecovery(next func(*journalRecovery, bool)) {
	job := w.beginCancellationRecovery()
	if job == nil {
		next(nil, false)
		return
	}
	go func() {
		timer := time.NewTimer(cancelJournalGrace)
		defer timer.Stop()
		pending := false
		select {
		case <-job.done:
		case <-timer.C:
			pending = true
		}
		w.dispatchRecovery(func() { next(job, pending) })
	}()
}

// The source renderer is already gone when this is used. Only release the
// session after the latest coalesced recovery, not an earlier completed read.
func (w *Window) releaseJournalAfterRecovery(job *journalRecovery) {
	if w.releaseJournalSession == nil {
		return
	}
	if job == nil {
		w.journalRelease.Do(w.releaseJournalSession)
		return
	}
	go func() {
		<-job.done
		if job.err != nil {
			return
		}
		w.dispatchRecovery(func() {
			w.journal.mu.Lock()
			next := w.journal.pending
			w.journal.mu.Unlock()
			if next != nil {
				w.releaseJournalAfterRecovery(next)
				return
			}
			w.journalRelease.Do(w.releaseJournalSession)
		})
	}()
}

func (w *Window) failAfterCancellationRecovery(err error, destroy func()) {
	w.rendererUnavailable.Store(true)
	if w.native != nil && !w.native.IsDestroyed() {
		w.hideWindow()
	}
	destroyOnce := sync.OnceFunc(destroy)
	if w.releaseJournalSession != nil {
		// An untrusted keeper owns another page of the same private session.
		// Destroy the failed source first, so no JS can commit a new cookie after
		// the final recovery scan but before session release.
		w.complete(err)
		destroyOnce()
		w.releaseJournalAfterRecovery(w.beginCancellationRecovery())
		return
	}
	w.afterCancellationRecovery(func(job *journalRecovery, pending bool) {
		w.complete(err) // Publish before Destroy's synchronous OnClosed callback.
		if job == nil || !pending && job.err == nil {
			destroyOnce()
			return
		}
		go func() {
			<-job.done
			if job.err == nil {
				w.dispatchRecovery(destroyOnce)
			}
		}()
	})
}

func (w *Window) stopCancellationRecovery() {
	if w.stopJournal != nil {
		w.stopJournal()
	}
}
