package desktopwindow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os/exec"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo"
)

func memoryJournalStorage(store map[string]string, mu *sync.Mutex) *journalStorage {
	return &journalStorage{
		list: func(context.Context) ([]journalStorageEntry, error) {
			mu.Lock()
			defer mu.Unlock()
			var names []string
			for name := range store {
				names = append(names, name)
			}
			sort.Strings(names)
			var entries []journalStorageEntry
			for _, name := range names {
				entries = append(entries, journalStorageEntry{Name: name, Value: store[name]})
			}
			return entries, nil
		},
		remove: func(_ context.Context, entry journalStorageEntry) error {
			mu.Lock()
			defer mu.Unlock()
			if current, ok := store[entry.Name]; ok && current != entry.Value {
				return errJournalEntryChanged
			}
			delete(store, entry.Name)
			return nil
		},
	}
}

func TestJournalBackupProgressSurvivesCookieReadFailureWithoutConfirmingEmpty(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	cookie := journalCookie(t, "scope", "backup", "native-request", "claimed-owner")
	store := map[string]string{cookie.Name: cookie.Value}
	var mu sync.Mutex
	var broken atomic.Bool
	broken.Store(true)
	cookieFailure := errors.New("cookie SDK temporarily unavailable")
	journal := &cancellationJournal{scope: "scope", backup: memoryJournalStorage(store, &mu), list: func() ([]mygo.Cookie, error) {
		if broken.Load() {
			return nil, cookieFailure
		}
		return nil, nil
	}}
	delivered := make(chan struct{})
	var calls atomic.Int32
	job := journal.recover(ctx, func(_ context.Context, request, operation string) error {
		if request != "native-request" || operation != "claimed-owner" {
			t.Error("backup retargeted cancellation", request, operation)
		}
		if calls.Add(1) == 1 {
			close(delivered)
		}
		return nil
	})
	select {
	case <-delivered:
	case <-time.After(time.Second):
		t.Fatal("cookie read failure blocked independent backup delivery")
	}
	select {
	case <-job.done:
		t.Fatal("failed source was treated as a confirmed empty journal")
	default:
	}
	broken.Store(false)
	waitJournal(t, job)
	mu.Lock()
	remaining := len(store)
	mu.Unlock()
	if calls.Load() != 1 || remaining != 0 {
		t.Fatal("backup was replayed or not acknowledged exactly", calls.Load(), remaining)
	}
}

func TestJournalReaderNotReadyStillDeliversCookiesAndRetainsSession(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	cookie := journalCookie(t, "scope", "cookie", "", "cookie-owner")
	backup := journalCookie(t, "scope", "backup", "", "backup-owner")
	var mu sync.Mutex
	cookies := []mygo.Cookie{cookie}
	store := map[string]string{backup.Name: backup.Value}
	storage := memoryJournalStorage(store, &mu)
	read := storage.list
	var ready atomic.Bool
	storage.list = func(ctx context.Context) ([]journalStorageEntry, error) {
		if !ready.Load() {
			return nil, errJournalReaderNotReady
		}
		return read(ctx)
	}
	journal := &cancellationJournal{scope: "scope", backup: storage, list: func() ([]mygo.Cookie, error) {
		mu.Lock()
		defer mu.Unlock()
		return append([]mygo.Cookie(nil), cookies...), nil
	}, remove: func(mygo.Cookie) error { mu.Lock(); cookies = nil; mu.Unlock(); return nil }}
	cookieDelivered := make(chan struct{})
	var calls atomic.Int32
	job := journal.recover(ctx, func(_ context.Context, _, operation string) error {
		calls.Add(1)
		if operation == "cookie-owner" {
			close(cookieDelivered)
		}
		return nil
	})
	released := make(chan struct{})
	w := &Window{done: make(chan struct{}), journal: journal, recoveryDispatch: func(fn func()) { fn() }, releaseJournalSession: func() { close(released) }}
	w.releaseJournalAfterRecovery(job)
	select {
	case <-cookieDelivered:
	case <-time.After(time.Second):
		t.Fatal("reader failure blocked healthy cookie source")
	}
	select {
	case <-released:
		t.Fatal("unready keeper caused profile loss")
	default:
	}
	ready.Store(true)
	select {
	case <-released:
	case <-time.After(5 * time.Second):
		t.Fatal("confirmed reader recovery did not drain and release")
	}
	if calls.Load() != 2 {
		t.Fatal("missing or duplicate recovered targets", calls.Load())
	}
}

func TestJournalDeduplicatesLogicalRecordsAcrossCookieAndBackupStores(t *testing.T) {
	cookie := journalCookie(t, "scope", "same-entry", "request", "operation")
	duplicate := journalCookie(t, "scope", "different-entry", "request", "operation")
	store := map[string]string{cookie.Name: cookie.Value, duplicate.Name: duplicate.Value}
	var mu sync.Mutex
	cookies := []mygo.Cookie{cookie}
	removedCookies, calls := 0, 0
	journal := &cancellationJournal{scope: "scope", backup: memoryJournalStorage(store, &mu), list: func() ([]mygo.Cookie, error) { return cookies, nil }, remove: func(value mygo.Cookie) error {
		if value.Name != cookie.Name {
			t.Error("wrong cookie deleted")
		}
		removedCookies++
		cookies = nil
		return nil
	}}
	waitJournal(t, journal.recover(context.Background(), func(_ context.Context, request, operation string) error {
		calls++
		if request != "request" || operation != "operation" {
			t.Error("wrong target")
		}
		return nil
	}))
	if calls != 1 || removedCookies != 1 || len(store) != 0 {
		t.Fatal("duplicate copies were not delivered once and deleted from both stores", calls, removedCookies, len(store))
	}
}

func TestJournalDuplicateFailedDeliveryIsAttemptedOnlyOncePerDrainPass(t *testing.T) {
	cookie := journalCookie(t, "scope", "same-entry", "request", "owner")
	store := map[string]string{cookie.Name: cookie.Value}
	var mu sync.Mutex
	cookies := []mygo.Cookie{cookie}
	journal := &cancellationJournal{scope: "scope", backup: memoryJournalStorage(store, &mu), list: func() ([]mygo.Cookie, error) { return cookies, nil }, remove: func(mygo.Cookie) error { cookies = nil; return nil }}
	acknowledged := make(map[string]string)
	calls := 0
	failure := errors.New("cleanup pending")
	processed, err := journal.replayBatch(func(context.Context, string, string) error { calls++; return failure }, context.Background(), acknowledged)
	if processed || !errors.Is(err, failure) || calls != 1 || len(store) != 1 || len(cookies) != 1 {
		t.Fatal("duplicate copies retried immediately or were erased without acknowledgment", processed, err, calls)
	}
	processed, err = journal.replayBatch(func(context.Context, string, string) error { calls++; return nil }, context.Background(), acknowledged)
	if !processed || err != nil || calls != 2 || len(store) != 0 || len(cookies) != 0 {
		t.Fatal("acknowledged retry failed to delete both copies exactly", processed, err, calls)
	}
}

func TestJournalPartialSourceReadsDeliverAvailableEntriesAndRetainErrors(t *testing.T) {
	cookie := journalCookie(t, "scope", "cookie", "", "cookie-owner")
	backup := journalCookie(t, "scope", "backup", "", "backup-owner")
	cookieFailure, readerFailure := errors.New("partial cookie read"), errors.New("partial backup read")
	journal := &cancellationJournal{scope: "scope", list: func() ([]mygo.Cookie, error) { return []mygo.Cookie{cookie}, cookieFailure }, remove: func(mygo.Cookie) error { return nil }, backup: &journalStorage{
		list: func(context.Context) ([]journalStorageEntry, error) {
			return []journalStorageEntry{{Name: backup.Name, Value: backup.Value}}, readerFailure
		}, remove: func(context.Context, journalStorageEntry) error { return nil },
	}}
	var targets []string
	processed, err := journal.replayBatch(func(_ context.Context, _, operation string) error { targets = append(targets, operation); return nil }, context.Background(), make(map[string]string))
	if !processed || !errors.Is(err, cookieFailure) || !errors.Is(err, readerFailure) || !reflect.DeepEqual(targets, []string{"backup-owner", "cookie-owner"}) {
		t.Fatal("partial journal data or source error was lost", processed, err, targets)
	}
}

func TestJournalBackupExactRemovalPreservesChangedValueAndDrainsNewTarget(t *testing.T) {
	old := journalCookie(t, "scope", "entry", "old-request", "old-owner")
	newer := journalCookie(t, "scope", "entry", "new-request", "new-owner")
	store := map[string]string{old.Name: old.Value}
	var mu sync.Mutex
	var targets []string
	journal := &cancellationJournal{scope: "scope", backup: memoryJournalStorage(store, &mu), list: func() ([]mygo.Cookie, error) { return nil, nil }}
	waitJournal(t, journal.recover(context.Background(), func(_ context.Context, request, operation string) error {
		targets = append(targets, request+"/"+operation)
		if operation == "old-owner" {
			mu.Lock()
			store[old.Name] = newer.Value
			mu.Unlock()
		}
		return nil
	}))
	if !reflect.DeepEqual(targets, []string{"old-request/old-owner", "new-request/new-owner"}) || len(store) != 0 {
		t.Fatal("old removal erased or retargeted newer backup", targets, store)
	}
}

func TestJournalBackupUsesStrictCookieDecoder(t *testing.T) {
	valid := journalCookie(t, "scope", "valid", "", "owner")
	foreign := journalCookie(t, "foreign", "invalid", "", "wrong-owner")
	store := map[string]string{valid.Name: valid.Value, foreign.Name: foreign.Value, cancelJournalPrefix + "scope-bad": "%7B%22scope%22%3A%22scope%22%2C%22request%22%3A%22%22%2C%22operation_id%22%3A%22wrong%22%2C%22token%22%3A%22forbidden%22%7D"}
	var mu sync.Mutex
	journal := &cancellationJournal{scope: "scope", backup: memoryJournalStorage(store, &mu), list: func() ([]mygo.Cookie, error) { return nil, nil }}
	var targets []string
	if err := journal.replay(func(_ context.Context, _, operation string) error { targets = append(targets, operation); return nil }, context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(targets, []string{"owner"}) || len(store) != 2 {
		t.Fatal("backup bypassed strict scope/schema checks", targets, store)
	}
}

// Exercise the actual host-generated scripts with a localStorage model, without
// starting a native app or loading the credential-bearing main page.
type scriptStorageState struct {
	Origin         string            `json:"origin"`
	Path           string            `json:"path"`
	Ready          bool              `json:"ready"`
	Marker         bool              `json:"marker"`
	Denied         bool              `json:"denied"`
	WriteRejected  bool              `json:"writeRejected"`
	StartupDelayMS int               `json:"startupDelayMS"`
	FailReadAfter  int               `json:"failReadAfter"`
	Store          map[string]string `json:"store"`
	Code           string            `json:"code"`
	Reads          []string          `json:"-"`
}

func scriptStorageEval(t *testing.T, state *scriptStorageState) func(context.Context, string) (any, error) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("reader script checks require Node.js")
	}
	return func(ctx context.Context, code string) (any, error) {
		state.Code = code
		input, err := json.Marshal(state)
		if err != nil {
			return nil, err
		}
		const harness = `const input=JSON.parse(process.argv[1]);const values=new Map(Object.entries(input.store));const reads=[];
		if(input.startupDelayMS)Atomics.wait(new Int32Array(new SharedArrayBuffer(4)),0,0,input.startupDelayMS);
		global.location={origin:input.origin,pathname:input.path};global.document={readyState:input.ready?'complete':'loading',querySelector:()=>input.marker?{content:'1'}:null};
		const storage={get length(){return values.size},key:i=>[...values.keys()][i],setItem:(name,value)=>{if(!input.writeRejected)values.set(name,value)},getItem:name=>{if(input.failReadAfter&&reads.length>=input.failReadAfter)throw new Error('temporary reader failure');reads.push(name);return values.has(name)?values.get(name):null},removeItem:name=>values.delete(name)};
		Object.defineProperty(global,'localStorage',{get(){if(input.denied){const error=new Error('storage denied');error.name='SecurityError';throw error}return storage}});
		const value=eval(input.code);process.stdout.write(JSON.stringify({value,store:Object.fromEntries(values),reads}));`
		// Native Eval's two-second timeout is not a budget for starting a cold
		// Node process. Keep this test-only interpreter separately bounded.
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		budget := 30 * time.Second
		if deadline, ok := t.Deadline(); ok && time.Until(deadline)-time.Second < budget {
			budget = time.Until(deadline) - time.Second
		}
		harnessCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), budget)
		defer stop()
		command := exec.CommandContext(harnessCtx, node, "-e", harness, string(input))
		var stderr bytes.Buffer
		command.Stderr = &stderr
		output, err := command.Output()
		if err != nil {
			return nil, fmt.Errorf("reader Node harness: %w (startup context: %v; native eval context: %v; stderr: %s)", err, harnessCtx.Err(), ctx.Err(), strings.TrimSpace(stderr.String()))
		}
		var result struct {
			Value string            `json:"value"`
			Store map[string]string `json:"store"`
			Reads []string          `json:"reads"`
		}
		if err := json.Unmarshal(output, &result); err != nil {
			return nil, err
		}
		state.Store, state.Reads = result.Store, result.Reads
		return result.Value, nil
	}
}

func TestInitialDeniedBackupDisablesScopeBeforeSourceLoadAndCookieRecovery(t *testing.T) {
	var probes, reads atomic.Int32
	storage := &journalStorage{
		negotiate: func(context.Context) (bool, error) { probes.Add(1); return false, nil },
		ready: func(context.Context) error {
			t.Error("disabled scope probed keeper again")
			return errJournalReaderNotReady
		},
		list: func(context.Context) ([]journalStorageEntry, error) {
			reads.Add(1)
			return nil, errJournalReaderNotReady
		},
	}
	backup := &negotiatedJournalBackup{storage: storage}
	const raw = "http://127.0.0.1:43821/#token=fixture&journal=scope&connect=request"
	for range 2 {
		source, err := prepareJournalSourceURL(context.Background(), raw, backup)
		parsed, parseErr := url.Parse(source)
		if err != nil || parseErr != nil || parsed.Fragment == "" {
			t.Fatal("source failed initial negotiation", err, parseErr)
		}
		fragment, _ := url.ParseQuery(parsed.Fragment)
		if fragment.Get("cancel_host") != "1" || fragment.Has("cancel_guard") || fragment.Get("journal_backup") != "0" || fragment.Get("journal") != "scope" || fragment.Get("token") != "fixture" || fragment.Get("connect") != "request" {
			t.Fatal("negotiation changed source identity or enabled denied backup", source)
		}
	}
	cookie := journalCookie(t, "scope", "cookie", "", "cookie-owner")
	cookies := []mygo.Cookie{cookie}
	journal := &cancellationJournal{scope: "scope", backup: backup.recoveryStore(), list: func() ([]mygo.Cookie, error) { return cookies, nil }, remove: func(mygo.Cookie) error { cookies = nil; return nil }}
	calls := 0
	ctx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	waitJournal(t, journal.recover(ctx, func(context.Context, string, string) error { calls++; return nil }))
	if probes.Load() != 1 || reads.Load() != 0 || calls != 1 {
		t.Fatal("permanently denied backup kept scope recovery alive or skipped cookie intent", probes.Load(), reads.Load(), calls)
	}
}

func TestEnabledBackupNeverDowngradesAfterSourceMayHaveWrittenRecords(t *testing.T) {
	var probes atomic.Int32
	var denied atomic.Bool
	backup := &negotiatedJournalBackup{storage: &journalStorage{
		negotiate: func(context.Context) (bool, error) { probes.Add(1); return !denied.Load(), nil },
		ready:     func(context.Context) error { return nil },
		list:      func(context.Context) ([]journalStorageEntry, error) { return nil, errJournalReaderNotReady },
	}}
	const raw = "http://127.0.0.1:43821/#token=fixture&journal=scope&journal_backup=0"
	for i := 0; i < 2; i++ {
		source, err := prepareJournalSourceURL(context.Background(), raw, backup)
		if err != nil {
			t.Fatal(err)
		}
		parsed, _ := url.Parse(source)
		fragment, _ := url.ParseQuery(parsed.Fragment)
		if fragment.Get("journal_backup") != "1" {
			t.Fatal("enabled scope changed mode", source)
		}
		denied.Store(true)
	}
	if _, err := backup.recoveryStore().list(context.Background()); !errors.Is(err, errJournalReaderNotReady) {
		t.Fatal("later enabled-store failure was mistaken for empty disabled backup", err)
	}
	if probes.Load() != 1 || backup.mode.Load() != journalBackupEnabled {
		t.Fatal("source reload renegotiated and discarded enabled-store responsibility", probes.Load(), backup.mode.Load())
	}
}

func TestEarlyNegotiationFailureCannotProduceAnAdmittedSourceURL(t *testing.T) {
	var reads atomic.Int32
	backup := &negotiatedJournalBackup{storage: &journalStorage{
		negotiate: func(context.Context) (bool, error) { return false, errJournalReaderNotReady },
		list: func(context.Context) ([]journalStorageEntry, error) {
			reads.Add(1)
			return nil, errJournalReaderNotReady
		},
	}}
	ctx, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stop()
	source, err := prepareJournalSourceURL(ctx, "http://127.0.0.1:43821/#journal=scope", backup)
	if source != "" || !errors.Is(err, context.DeadlineExceeded) || backup.mode.Load() != journalBackupUnknown {
		t.Fatal("unconfirmed initial reader was turned into an admitted mode", source, err, backup.mode.Load())
	}
	journal := &cancellationJournal{scope: "scope", backup: backup.recoveryStore(), list: func() ([]mygo.Cookie, error) { return nil, nil }}
	waitJournal(t, journal.recover(context.Background(), func(context.Context, string, string) error {
		t.Error("early failure invented a cancellation")
		return nil
	}))
	if reads.Load() != 0 {
		t.Fatal("pre-admission failure retained an unreadable store that no source could write")
	}
}

func TestNativeRecoveryReaderNegotiatesUsabilityWithVerifiedScopedProbe(t *testing.T) {
	for _, mode := range []string{"usable", "denied", "silent-rejection", "not-ready"} {
		t.Run(mode, func(t *testing.T) {
			state := &scriptStorageState{Origin: "http://127.0.0.1:43821", Path: journalReaderPath, Ready: true, Marker: true, Store: map[string]string{"unrelated": "retained"}}
			state.Denied = mode == "denied"
			state.WriteRejected = mode == "silent-rejection"
			state.Ready = mode != "not-ready"
			reader := newJournalStorageReader("scope", state.Origin, scriptStorageEval(t, state))
			usable, err := reader.negotiate(context.Background())
			if mode == "not-ready" {
				if !errors.Is(err, errJournalReaderNotReady) {
					t.Fatal("unready document became a definitive disabled mode", usable, err)
				}
			} else if err != nil || usable != (mode == "usable") {
				t.Fatal("incorrect initial storage decision", usable, err)
			}
			if !reflect.DeepEqual(state.Store, map[string]string{"unrelated": "retained"}) {
				t.Fatal("availability probe changed unrelated storage or left a cancellation-shaped marker", state.Store)
			}
			for _, name := range state.Reads {
				if !strings.HasPrefix(name, cancelJournalPrefix+"scope-probe-") {
					t.Fatal("probe read unscoped data", name)
				}
			}
		})
	}
}

func TestNativeRecoveryReaderNormalizesDefaultPortWithoutExpandingOrigin(t *testing.T) {
	for _, test := range []struct {
		name, origin, browserOrigin string
		allowed                     bool
	}{
		{"explicit-default", "http://127.0.0.1:80", "http://127.0.0.1", true},
		{"normalized-default", "http://127.0.0.1", "http://127.0.0.1", true},
		{"nondefault", navigationTestOrigin, navigationTestOrigin, true},
		{"different-port", "http://127.0.0.1:80", "http://127.0.0.1:81", false},
		{"different-host", "http://127.0.0.1:80", "http://localhost", false},
		{"host-prefix-spoof", "http://127.0.0.1:80", "http://127.0.0.1.evil.example", false},
		{"different-scheme", "http://127.0.0.1:80", "https://127.0.0.1", false},
		{"nondefault-missing-port", navigationTestOrigin, "http://127.0.0.1", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			entry := journalCookie(t, "scope", "entry", "request", "owner")
			foreign := journalCookie(t, "other", "entry", "", "foreign-owner")
			state := &scriptStorageState{Origin: test.browserOrigin, Path: journalReaderPath, Ready: true, Marker: true, Store: map[string]string{entry.Name: entry.Value, foreign.Name: foreign.Value, "tailsplit-config-token": "fixture-must-not-read"}}
			original := maps.Clone(state.Store)
			reader := newJournalStorageReader("scope", test.origin, scriptStorageEval(t, state))
			readyErr := reader.ready(context.Background())
			usable, negotiateErr := reader.negotiate(context.Background())
			entries, listErr := reader.list(context.Background())
			reads := append([]string(nil), state.Reads...)
			removeErr := reader.remove(context.Background(), journalStorageEntry{Name: entry.Name, Value: entry.Value})
			if !test.allowed {
				for _, err := range []error{readyErr, negotiateErr, listErr, removeErr} {
					if !errors.Is(err, errJournalReaderNotReady) {
						t.Fatal("foreign origin passed recovery guard", err)
					}
				}
				if usable || len(entries) != 0 || len(reads) != 0 || len(state.Reads) != 0 || !reflect.DeepEqual(state.Store, original) {
					t.Fatal("foreign origin accessed recovery storage", entries, reads, state.Reads)
				}
				return
			}
			if readyErr != nil || negotiateErr != nil || listErr != nil || removeErr != nil || !usable {
				t.Fatal("same origin could not negotiate/read/acknowledge recovery", readyErr, negotiateErr, listErr, removeErr, usable)
			}
			if !reflect.DeepEqual(entries, []journalStorageEntry{{Name: entry.Name, Value: entry.Value}}) || !reflect.DeepEqual(reads, []string{entry.Name}) {
				t.Fatal("normalization bypassed scoped reads", entries, reads)
			}
			delete(original, entry.Name)
			if !reflect.DeepEqual(state.Store, original) {
				t.Fatal("normalization changed unrelated storage", state.Store)
			}
		})
	}
}

func TestNativeRecoveryReaderHarnessStartupDoesNotUseProductionEvalDeadline(t *testing.T) {
	state := &scriptStorageState{Origin: "http://127.0.0.1:43821", Path: journalReaderPath, Ready: true, Marker: true, Store: map[string]string{}, StartupDelayMS: int(journalReaderTimeout/time.Millisecond) + 100}
	reader := newJournalStorageReader("scope", state.Origin, scriptStorageEval(t, state))
	if err := reader.ready(context.Background()); err != nil {
		t.Fatal("cold interpreter startup consumed native Eval timeout", err)
	}
}

func TestNativeRecoveryReaderScriptsReadOnlyScopedEntriesAndCompareBeforeDelete(t *testing.T) {
	entry := journalCookie(t, "scope", "entry", "request", "owner")
	foreign := journalCookie(t, "other", "entry", "", "wrong")
	state := &scriptStorageState{Origin: "http://127.0.0.1:43821", Path: journalReaderPath, Ready: true, Marker: true, Store: map[string]string{entry.Name: entry.Value, foreign.Name: foreign.Value, "tailsplit-config-token": "must-not-read"}}
	reader := newJournalStorageReader("scope", state.Origin, scriptStorageEval(t, state))
	entries, err := reader.list(context.Background())
	if err != nil || !reflect.DeepEqual(entries, []journalStorageEntry{{Name: entry.Name, Value: entry.Value}}) || !reflect.DeepEqual(state.Reads, []string{entry.Name}) {
		t.Fatal("reader leaked unrelated storage", entries, err, state.Reads)
	}
	state.Store[entry.Name] = "new-value"
	if err := reader.remove(context.Background(), entries[0]); !errors.Is(err, errJournalEntryChanged) || state.Store[entry.Name] != "new-value" {
		t.Fatal("old acknowledgment erased newer value", err, state.Store)
	}
	state.Store[entry.Name] = entry.Value
	if err := reader.remove(context.Background(), entries[0]); err != nil {
		t.Fatal(err)
	}
	if _, ok := state.Store[entry.Name]; ok || len(state.Store) != 2 {
		t.Fatal("exact removal changed wrong keys", state.Store)
	}
}

func TestNativeRecoveryReaderRejectsWrongPageUnreadyAndDeniedStorage(t *testing.T) {
	state := &scriptStorageState{Origin: "http://127.0.0.1:43821", Path: journalReaderPath, Ready: true, Marker: true, Store: map[string]string{}}
	reader := newJournalStorageReader("scope", state.Origin, scriptStorageEval(t, state))
	for _, change := range []func(){func() { state.Ready = false }, func() { state.Ready = true; state.Path = "/" }, func() { state.Path = journalReaderPath; state.Marker = false }, func() { state.Marker = true; state.Origin = "http://foreign.example" }} {
		change()
		if _, err := reader.list(context.Background()); !errors.Is(err, errJournalReaderNotReady) {
			t.Fatal("unavailable reader confirmed empty journal", err)
		}
	}
	state.Origin = "http://127.0.0.1:43821"
	state.Denied = true
	if err := reader.ready(context.Background()); err != nil {
		t.Fatal("storage denial should not prevent loading the static keeper document", err)
	}
	if _, err := reader.list(context.Background()); !errors.Is(err, errJournalReaderNotReady) {
		t.Fatal("denied source confirmed an empty journal", err)
	}
}

func TestNativeRecoveryReaderReturnsPartialEntriesAlongsideReadFailure(t *testing.T) {
	first := journalCookie(t, "scope", "first", "", "first-owner")
	second := journalCookie(t, "scope", "second", "", "second-owner")
	state := &scriptStorageState{Origin: "http://127.0.0.1:43821", Path: journalReaderPath, Ready: true, Marker: true, FailReadAfter: 1, Store: map[string]string{first.Name: first.Value, second.Name: second.Value}}
	reader := newJournalStorageReader("scope", state.Origin, scriptStorageEval(t, state))
	entries, err := reader.list(context.Background())
	if !errors.Is(err, errJournalReaderNotReady) || len(entries) != 1 {
		t.Fatal("partial read either lost its entry or falsely confirmed completion", entries, err)
	}
}
