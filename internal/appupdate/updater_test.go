package appupdate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type requestLog struct {
	mu   sync.Mutex
	urls []string
}

func (l *requestLog) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.urls)
}

// Rewrite only inside the private test transport: all updater URL validation
// still sees the real official HTTPS URLs, including redirect destinations.
func testUpdater(t *testing.T, handler http.HandlerFunc) (*Updater, *requestLog) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	local, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	t.Cleanup(transport.CloseIdleConnections)
	log := new(requestLog)
	u := New("0.6.1", true)
	u.goos, u.goarch = "darwin", "arm64"
	u.tempRoot = t.TempDir()
	u.requestTimeout = 2 * time.Second
	u.downloadTimeout = 3 * time.Second
	u.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		log.mu.Lock()
		log.urls = append(log.urls, request.URL.String())
		log.mu.Unlock()
		copy := request.Clone(request.Context())
		address := *request.URL
		address.Scheme, address.Host = local.Scheme, local.Host
		copy.URL = &address
		copy.Host = request.URL.Host
		response, err := transport.RoundTrip(copy)
		if response != nil {
			response.Request = request
		}
		return response, err
	})}
	return u, log
}

type releaseFixture struct {
	release githubRelease
	payload []byte
	sums    []byte
}

func fixture(t *testing.T, tag, goos, goarch string) releaseFixture {
	t.Helper()
	name, err := desktopAssetName(strings.TrimPrefix(tag, "v"), goos, goarch)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("small inert desktop package fixture\n")
	sums := []byte(fmt.Sprintf("%x  %s\n", sha256.Sum256(payload), name))
	return releaseFixture{
		release: githubRelease{
			Tag: tag, URL: releasePageBase + tag, Notes: "Release notes as plain text.",
			Assets: []releaseAsset{
				{Name: name, URL: assetURLBase + tag + "/" + name, Size: int64(len(payload))},
				{Name: "SHA256SUMS", URL: assetURLBase + tag + "/SHA256SUMS", Size: int64(len(sums))},
			},
		},
		payload: payload,
		sums:    sums,
	}
}

func (f releaseFixture) serve(w http.ResponseWriter, request *http.Request) {
	switch request.URL.Path {
	case "/idy/tailsplit/releases/latest":
		w.Header().Set("Location", f.release.URL)
		w.WriteHeader(http.StatusFound)
	case "/repos/idy/tailsplit/releases/latest":
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(f.release)
	case "/idy/tailsplit/releases/download/" + f.release.Tag + "/" + f.release.Assets[0].Name:
		_, _ = w.Write(f.payload)
	case "/idy/tailsplit/releases/download/" + f.release.Tag + "/SHA256SUMS":
		_, _ = w.Write(f.sums)
	default:
		http.NotFound(w, request)
	}
}

func requireCheck(t *testing.T, u *Updater) {
	t.Helper()
	if err := u.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSemverOrdering(t *testing.T) {
	for _, test := range []struct {
		current, latest string
		available       bool
	}{
		{"0.6.1", "v0.6.2", true},
		{"0.6.1", "v0.8.0", true},
		{"v0.6.9", "v0.6.10", true},
		{"0.9.0", "v0.10.0", true},
		{"1.9.0", "v1.10.0", true},
		{"1.99.0", "v2.0.0", true},
		{"0.6.2", "v0.6.2", false},
		{"0.7.0", "v0.6.99", false},
		{"0.6.2+build.1", "v0.6.2+build.2", false},
		{"0.6.1", "0.6.2", true},
	} {
		t.Run(test.current+"_to_"+test.latest, func(t *testing.T) {
			f := fixture(t, test.latest, "darwin", "arm64")
			u, _ := testUpdater(t, f.serve)
			u.state.CurrentVersion = test.current
			u.current, _ = stableVersion(test.current)
			checked := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
			u.now = func() time.Time { return checked }
			requireCheck(t, u)
			state := u.Snapshot()
			if state.Available != test.available || state.LatestVersion != strings.TrimPrefix(test.latest, "v") ||
				state.CurrentVersion != test.current || state.Checking || state.Error != "" || state.LastChecked != checked {
				t.Fatalf("unexpected state: %+v", state)
			}
		})
	}
}

func TestRejectUnstableOrInvalidRelease(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*githubRelease)
	}{
		{"draft", func(r *githubRelease) { r.Draft = true }},
		{"prerelease flag", func(r *githubRelease) { r.Prerelease = true }},
		{"prerelease version", func(r *githubRelease) { r.Tag = "v0.7.0-beta.1" }},
		{"short version", func(r *githubRelease) { r.Tag = "v0.7" }},
		{"malformed version", func(r *githubRelease) { r.Tag = "v0.06.2" }},
		{"foreign page", func(r *githubRelease) { r.URL = "https://github.com/foreign/tailsplit/releases/tag/v0.6.2" }},
		{"HTTP page", func(r *githubRelease) { r.URL = "http://github.com/idy/tailsplit/releases/tag/v0.6.2" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := fixture(t, "v0.6.2", "darwin", "arm64")
			test.change(&f.release)
			u, log := testUpdater(t, f.serve)
			if err := u.Check(context.Background()); err == nil {
				t.Fatal("accepted invalid release")
			}
			state := u.Snapshot()
			if state.Available || state.Error == "" || state.Checking || log.count() != 1 {
				t.Fatalf("unexpected failed check: %+v; requests=%d", state, log.count())
			}
		})
	}
}

func TestDevelopmentVersionsMakeNoRequests(t *testing.T) {
	for _, version := range []string{"", "dev", "(devel)", "v0.6", "0.6.2-dev", "0.6.2-dirty", "v01.6.2"} {
		t.Run(version, func(t *testing.T) {
			u, log := testUpdater(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected request") })
			u.state.CurrentVersion = version
			u.current, _ = stableVersion(version)
			if err := u.Check(context.Background()); err == nil {
				t.Fatal("development check succeeded")
			}
			if log.count() != 0 || u.Snapshot().Available {
				t.Fatal("development build contacted release source")
			}
		})
	}
}

func TestSelectOnlyMatchingDesktopAsset(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Updater, *githubRelease)
	}{
		{"CLI only", func(u *Updater, r *githubRelease) { r.Assets[0].Name = "tailsplit_0.6.2_darwin_arm64.tar.gz" }},
		{"other OS", func(u *Updater, r *githubRelease) { r.Assets[0].Name = "TailSplit_0.6.2_windows_arm64_setup.exe" }},
		{"other arch", func(u *Updater, r *githubRelease) { r.Assets[0].Name = "TailSplit_0.6.2_darwin_amd64.zip" }},
		{"other version", func(u *Updater, r *githubRelease) { r.Assets[0].Name = "TailSplit_0.6.3_darwin_arm64.zip" }},
		{"duplicate package", func(u *Updater, r *githubRelease) { r.Assets = append(r.Assets, r.Assets[0]) }},
		{"missing sums", func(u *Updater, r *githubRelease) { r.Assets = r.Assets[:1] }},
		{"duplicate sums", func(u *Updater, r *githubRelease) { r.Assets = append(r.Assets, r.Assets[1]) }},
		{"unsupported OS", func(u *Updater, r *githubRelease) { u.goos = "linux" }},
		{"unsupported arch", func(u *Updater, r *githubRelease) { u.goarch = "386" }},
		{"zero size", func(u *Updater, r *githubRelease) { r.Assets[0].Size = 0 }},
		{"oversized package", func(u *Updater, r *githubRelease) { r.Assets[0].Size = maxPackageBytes + 1 }},
		{"oversized sums", func(u *Updater, r *githubRelease) { r.Assets[1].Size = maxSumsBytes + 1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := fixture(t, "v0.6.2", "darwin", "arm64")
			var u *Updater
			u, _ = testUpdater(t, func(w http.ResponseWriter, r *http.Request) { f.serve(w, r) })
			test.change(u, &f.release)
			if err := u.Check(context.Background()); err == nil || u.Snapshot().Available {
				t.Fatalf("accepted invalid package: err=%v, state=%+v", err, u.Snapshot())
			}
		})
	}
}

func TestAssetMetadataURLTrust(t *testing.T) {
	const name = "TailSplit_0.6.2_darwin_arm64.zip"
	for _, raw := range []string{
		"http://github.com/idy/tailsplit/releases/download/v0.6.2/" + name,
		"https://evil.example/idy/tailsplit/releases/download/v0.6.2/" + name,
		"https://github.com.evil.example/idy/tailsplit/releases/download/v0.6.2/" + name,
		"https://github.com/foreign/tailsplit/releases/download/v0.6.2/" + name,
		"https://github.com/idy/tailsplit/releases/download/v0.6.3/" + name,
		"https://github.com/idy/tailsplit/releases/download/v0.6.2/tailsplit.zip",
		"https://github.com:443/idy/tailsplit/releases/download/v0.6.2/" + name,
		"https://user@github.com/idy/tailsplit/releases/download/v0.6.2/" + name,
		assetURLBase + "v0.6.2/" + name + "?download=1",
		assetURLBase + "v0.6.2/" + name + "#fragment",
		assetURLBase + "v0.6.2/%54ailSplit_0.6.2_darwin_arm64.zip",
	} {
		t.Run(raw, func(t *testing.T) {
			f := fixture(t, "v0.6.2", "darwin", "arm64")
			f.release.Assets[0].URL = raw
			u, log := testUpdater(t, f.serve)
			if err := u.Check(context.Background()); err == nil || log.count() != 1 {
				t.Fatalf("untrusted URL accepted: %v; requests=%d", err, log.count())
			}
		})
	}
	// The manifest must have the same strict repository, tag and filename pin.
	f := fixture(t, "v0.6.2", "darwin", "arm64")
	f.release.Assets[1].URL = "https://github.com/foreign/tailsplit/releases/download/v0.6.2/SHA256SUMS"
	u, _ := testUpdater(t, f.serve)
	if err := u.Check(context.Background()); err == nil {
		t.Fatal("accepted foreign manifest")
	}
}

func TestFailurePreservesOfferAndSuccessClearsStaleState(t *testing.T) {
	f := fixture(t, "v0.6.2", "darwin", "arm64")
	var checks atomic.Int32
	u, _ := testUpdater(t, func(w http.ResponseWriter, r *http.Request) {
		switch checks.Add(1) {
		case 1:
			f.serve(w, r)
		case 2:
			http.Error(w, "offline", http.StatusServiceUnavailable)
		default:
			current := f.release
			current.Tag, current.URL, current.Notes = "v0.6.1", releasePageBase+"v0.6.1", "Current release"
			_ = json.NewEncoder(w).Encode(current)
		}
	})
	requireCheck(t, u)
	before := u.Snapshot()
	if err := u.Check(context.Background()); err == nil {
		t.Fatal("expected failed recheck")
	}
	after := u.Snapshot()
	if !after.Available || after.LatestVersion != before.LatestVersion || after.ReleaseURL != before.ReleaseURL ||
		after.ReleaseNotes != before.ReleaseNotes || after.Error == "" || after.Checking {
		t.Fatalf("failed recheck lost offer: %+v", after)
	}
	requireCheck(t, u)
	after = u.Snapshot()
	if after.Available || after.LatestVersion != "0.6.1" || after.Error != "" || u.offer != nil {
		t.Fatalf("successful current-version check retained stale offer: %+v", after)
	}
	if _, err := u.Download(context.Background()); err == nil {
		t.Fatal("download accepted cleared offer")
	}
}

func TestCheckHTTPFailuresAndLimits(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusInternalServerError, http.StatusForbidden, http.StatusTooManyRequests} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			u, _ := testUpdater(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-RateLimit-Remaining", "0")
				http.Error(w, "not copied into state", status)
			})
			err := u.Check(context.Background())
			if err == nil || !strings.Contains(err.Error(), fmt.Sprint(status)) || strings.Contains(err.Error(), "not copied") {
				t.Fatalf("unexpected HTTP error: %v", err)
			}
			if (status == 403 || status == 429) && !strings.Contains(err.Error(), "rate limit") {
				t.Fatalf("rate limit is not explained: %v", err)
			}
		})
	}
	for _, test := range []struct {
		name string
		body []byte
	}{
		{"invalid JSON", []byte("not JSON")},
		{"oversized JSON", bytes.Repeat([]byte(" "), maxJSONBytes+1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			u, _ := testUpdater(t, func(w http.ResponseWriter, r *http.Request) {
				w.(http.Flusher).Flush() // Unknown Content-Length must still be bounded.
				_, _ = w.Write(test.body)
			})
			if err := u.Check(context.Background()); err == nil {
				t.Fatal("accepted invalid/oversized metadata")
			}
		})
	}
	t.Run("offline", func(t *testing.T) {
		u := New("0.6.1", true)
		u.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("offline fixture") })
		if err := u.Check(context.Background()); err == nil || !strings.Contains(err.Error(), "offline fixture") {
			t.Fatalf("unexpected offline error: %v", err)
		}
	})
	t.Run("source pin", func(t *testing.T) {
		u, log := testUpdater(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected foreign request") })
		u.source = "https://api.github.com/repos/foreign/tailsplit/releases/latest"
		if err := u.Check(context.Background()); err == nil || log.count() != 0 {
			t.Fatal("metadata source pin was bypassed")
		}
	})
	t.Run("metadata redirect", func(t *testing.T) {
		u, log := testUpdater(t, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://api.github.com/repos/foreign/tailsplit/releases/latest", http.StatusFound)
		})
		if err := u.Check(context.Background()); err == nil || log.count() != 1 {
			t.Fatal("followed metadata redirect")
		}
	})
}

func TestCheckCancellationAndDeadline(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(fmt.Sprint("deadline=", deadline), func(t *testing.T) {
			started := make(chan struct{})
			u, _ := testUpdater(t, func(w http.ResponseWriter, r *http.Request) {
				close(started)
				<-r.Context().Done()
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if deadline {
				u.requestTimeout = 50 * time.Millisecond
			}
			done := make(chan error, 1)
			go func() { done <- u.Check(ctx) }()
			waitSignal(t, started)
			if !deadline {
				cancel()
			}
			err := waitError(t, done)
			expected := error(context.Canceled)
			if deadline {
				expected = context.DeadlineExceeded
			}
			if !errors.Is(err, expected) || u.Snapshot().Checking || u.Snapshot().Error == "" {
				t.Fatalf("cancellation not reflected: err=%v state=%+v", err, u.Snapshot())
			}
		})
	}
}

func TestReleaseNotesAreBoundedPlainText(t *testing.T) {
	f := fixture(t, "v0.6.2", "darwin", "arm64")
	f.release.Notes = "<b>literal release note</b>\n" + strings.Repeat("界", maxNotesBytes)
	u, _ := testUpdater(t, f.serve)
	requireCheck(t, u)
	notes := u.Snapshot().ReleaseNotes
	if len(notes) > maxNotesBytes || !utf8.ValidString(notes) || !strings.HasPrefix(notes, "<b>literal release note</b>\n") {
		t.Fatal("notes were not safely bounded as plain text")
	}
}

func TestStateJSONAndIndependentSnapshot(t *testing.T) {
	u := New("0.6.1", true)
	state := u.Snapshot()
	state.Installing, state.Automatic = true, false
	if u.Snapshot().Installing || !u.Snapshot().Automatic {
		t.Fatal("snapshot mutation changed updater state")
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"current_version", "latest_version", "automatic", "checking", "downloading", "installing", "available", "downloaded_bytes", "total_bytes", "last_checked", "error", "release_url", "release_notes"} {
		if _, ok := fields[name]; !ok {
			t.Errorf("missing JSON field %s", name)
		}
	}
}

func waitSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for operation")
	}
}

func waitError(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for operation result")
		return nil
	}
}

func TestReadBoundedDeclaredLength(t *testing.T) {
	response := &http.Response{ContentLength: maxJSONBytes + 1, Body: io.NopCloser(strings.NewReader("small"))}
	if _, err := readBounded(response, maxJSONBytes); err == nil {
		t.Fatal("accepted excessive declared response length")
	}
}

func TestConditionalReleaseReusesValidatedOffer(t *testing.T) {
	f := fixture(t, "v0.6.2", "darwin", "arm64")
	var checks atomic.Int32
	u, _ := testUpdater(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/idy/tailsplit/releases/latest" {
			if checks.Add(1) == 1 {
				if r.Header.Get("If-None-Match") != "" {
					t.Error("initial request sent an ETag")
				}
				w.Header().Set("ETag", `W/"release-1"`)
			} else {
				if r.Header.Get("If-None-Match") != `W/"release-1"` {
					t.Errorf("missing conditional validator: %q", r.Header.Get("If-None-Match"))
				}
				// A weak and strong ETag with the same opaque value compare equal
				// for If-None-Match. A 304 need not carry a response body.
				w.Header().Set("ETag", `"release-1"`)
				w.WriteHeader(http.StatusNotModified)
				return
			}
		} else if r.Header.Get("If-None-Match") != "" {
			t.Error("metadata ETag leaked into an asset request")
		}
		f.serve(w, r)
	})
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	u.now = func() time.Time { return now }
	requireCheck(t, u)
	before, offer := u.Snapshot(), *u.offer
	now = now.Add(5 * time.Minute)
	requireCheck(t, u)
	after := u.Snapshot()
	if !after.Available || after.LatestVersion != before.LatestVersion || after.ReleaseURL != before.ReleaseURL ||
		after.ReleaseNotes != before.ReleaseNotes || *u.offer != offer || after.Error != "" || !after.LastChecked.Equal(now) {
		t.Fatalf("304 lost validated offer or check state: %+v", after)
	}
	pkg, err := u.Download(context.Background())
	if err != nil {
		t.Fatal("cached offer was not downloadable:", err)
	}
	defer pkg.Cleanup()
}

func TestRejectUnusable304(t *testing.T) {
	for _, test := range []struct {
		name        string
		prime       bool
		firstETag   string
		body        string
		invalidURL  bool
		firstOK     bool
		responseTag string
	}{
		{name: "initial"},
		{name: "no validator", prime: true, firstOK: true},
		{name: "malformed validator", prime: true, firstOK: true, firstETag: "not-an-etag"},
		{name: "invalid JSON was not cached", prime: true, firstETag: `"bad"`, body: "{"},
		{name: "invalid asset was not cached", prime: true, firstETag: `"bad"`, invalidURL: true},
		{name: "mismatched 304 validator", prime: true, firstETag: `"good"`, firstOK: true, responseTag: `"different"`},
		{name: "malformed 304 validator", prime: true, firstETag: `"good"`, firstOK: true, responseTag: "not-an-etag"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := fixture(t, "v0.6.2", "darwin", "arm64")
			if test.invalidURL {
				f.release.Assets[0].URL = "https://evil.example/package.zip"
			}
			var checks atomic.Int32
			u, log := testUpdater(t, func(w http.ResponseWriter, r *http.Request) {
				if checks.Add(1) == 1 && test.prime {
					w.Header().Set("ETag", test.firstETag)
					if test.body != "" {
						_, _ = io.WriteString(w, test.body)
					} else {
						f.serve(w, r)
					}
					return
				}
				if !test.firstOK && r.Header.Get("If-None-Match") != "" {
					t.Error("unvalidated metadata supplied a conditional validator")
				}
				w.Header().Set("ETag", test.responseTag)
				w.WriteHeader(http.StatusNotModified)
			})
			if test.prime {
				if err := u.Check(context.Background()); (err == nil) != test.firstOK {
					t.Fatalf("unexpected priming result: %v", err)
				}
			}
			if err := u.Check(context.Background()); err == nil || !strings.Contains(err.Error(), "304") {
				t.Fatalf("accepted unusable 304: %v", err)
			}
			wanted := 1
			if test.prime {
				wanted++
			}
			if log.count() != wanted || u.Snapshot().Available != test.firstOK || u.Snapshot().Checking {
				t.Fatalf("bad 304 changed offer or retried: state=%+v requests=%d", u.Snapshot(), log.count())
			}
		})
	}
}

func TestInvalidReplacementDoesNotPoisonConditionalCache(t *testing.T) {
	f := fixture(t, "v0.6.2", "darwin", "arm64")
	var checks atomic.Int32
	u, _ := testUpdater(t, func(w http.ResponseWriter, r *http.Request) {
		switch checks.Add(1) {
		case 1:
			w.Header().Set("ETag", `"validated"`)
			f.serve(w, r)
		case 2:
			w.Header().Set("ETag", `"invalid"`)
			invalid := f.release
			invalid.Prerelease = true
			_ = json.NewEncoder(w).Encode(invalid)
		default:
			if r.Header.Get("If-None-Match") != `"validated"` {
				t.Error("failed validation replaced the cached ETag")
			}
			w.WriteHeader(http.StatusNotModified)
		}
	})
	requireCheck(t, u)
	if err := u.Check(context.Background()); err == nil {
		t.Fatal("accepted invalid replacement")
	}
	requireCheck(t, u)
	if !u.Snapshot().Available || u.Snapshot().Error != "" {
		t.Fatal("304 did not restore the last validated offer")
	}
}

func TestAutomaticDiscoveryAvoidsSharedIPAPIQuota(t *testing.T) {
	f := fixture(t, "v0.6.2", "darwin", "arm64")
	var api, probes atomic.Int32
	var clients []*Updater
	for range 6 {
		u, _ := testUpdater(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/idy/tailsplit/releases/latest" {
				probes.Add(1)
				if r.Method != http.MethodHead || r.Header.Get("If-None-Match") != "" {
					t.Error("discovery fetched a page or leaked an API validator")
				}
			} else {
				api.Add(1)
				w.Header().Set("ETag", `"`+f.release.Tag+`"`)
			}
			f.serve(w, r)
		})
		// Include up-to-date clients, whose validated cached result has no offer.
		if len(clients) >= 3 {
			u.current = "v0.6.2"
			u.state.CurrentVersion = "0.6.2"
		}
		clients = append(clients, u)
		if err := u.checkForUpdates(context.Background(), true); err != nil {
			t.Fatal(err)
		}
	}
	// Six clients each perform twelve five-minute checks behind one IP. Only
	// their initial metadata fetches consume the 60/hour unauthenticated quota.
	for range 12 {
		for _, u := range clients {
			if err := u.checkForUpdates(context.Background(), true); err != nil {
				t.Fatal(err)
			}
		}
	}
	if api.Load() != 6 || probes.Load() != 72 {
		t.Fatalf("unchanged releases consumed API quota: API=%d probes=%d", api.Load(), probes.Load())
	}
	f = fixture(t, "v0.6.3", "darwin", "arm64")
	for _, u := range clients {
		if err := u.checkForUpdates(context.Background(), true); err != nil {
			t.Fatal(err)
		}
		if !u.Snapshot().Available || u.Snapshot().LatestVersion != "0.6.3" {
			t.Fatal("changed redirect did not fetch validated metadata")
		}
	}
	if api.Load() != 12 || probes.Load() != 78 {
		t.Fatalf("changed tags did not fetch exactly once: API=%d probes=%d", api.Load(), probes.Load())
	}
}

func TestManualCheckRefreshesSameTagEdits(t *testing.T) {
	f := fixture(t, "v0.6.2", "darwin", "arm64")
	var api atomic.Int32
	u, _ := testUpdater(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/idy/tailsplit/releases/latest" {
			if api.Add(1) > 1 && r.Header.Get("If-None-Match") != `"old"` {
				t.Error("manual check did not revalidate metadata")
			}
			w.Header().Set("ETag", `"old"`)
		}
		f.serve(w, r)
	})
	requireCheck(t, u)
	before := u.Snapshot().ReleaseNotes
	f.release.Notes = "Updated notes for the same tag"
	if err := u.checkForUpdates(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if api.Load() != 1 || u.Snapshot().ReleaseNotes != before {
		t.Fatal("automatic check unnecessarily refreshed an unchanged tag")
	}
	u.SetAutomatic(false)
	requireCheck(t, u)
	if api.Load() != 2 || u.Snapshot().ReleaseNotes != f.release.Notes || u.Snapshot().Automatic {
		t.Fatal("manual check did not refresh same-tag edits independently of preference")
	}
}

func TestDiscoveryRedirectTrust(t *testing.T) {
	for _, test := range []struct {
		name     string
		status   int
		location string
	}{
		{"not a redirect", http.StatusOK, releasePageBase + "v0.6.2"},
		{"initial web 304", http.StatusNotModified, ""},
		{"missing location", http.StatusFound, ""},
		{"relative", http.StatusFound, "/idy/tailsplit/releases/tag/v0.6.2"},
		{"foreign host", http.StatusFound, "https://evil.example/idy/tailsplit/releases/tag/v0.6.2"},
		{"foreign repository", http.StatusFound, "https://github.com/foreign/tailsplit/releases/tag/v0.6.2"},
		{"HTTP", http.StatusFound, "http://github.com/idy/tailsplit/releases/tag/v0.6.2"},
		{"query", http.StatusFound, releasePageBase + "v0.6.2?x=1"},
		{"fragment", http.StatusFound, releasePageBase + "v0.6.2#notes"},
		{"encoded tag", http.StatusFound, releasePageBase + "%760.6.2"},
		{"prerelease", http.StatusFound, releasePageBase + "v0.6.3-beta.1"},
		{"loop", http.StatusFound, latestReleasePage},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := fixture(t, "v0.6.2", "darwin", "arm64")
			u, log := testUpdater(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/idy/tailsplit/releases/latest" {
					w.Header().Set("Location", test.location)
					w.WriteHeader(test.status)
					return
				}
				f.serve(w, r)
			})
			requireCheck(t, u)
			if err := u.checkForUpdates(context.Background(), true); err == nil {
				t.Fatal("accepted untrusted or invalid discovery response")
			}
			if log.count() != 2 || !u.Snapshot().Available {
				t.Fatal("invalid discovery followed a redirect, fell back to the API, or lost the offer")
			}
		})
	}
}

func TestCheckRateLimitBackoff(t *testing.T) {
	base := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name       string
		status     int
		remaining  string
		retryAfter string
		reset      string
		delay      time.Duration
		limited    bool
	}{
		{"primary 403", 403, "0", "", fmt.Sprint(base.Add(17 * time.Minute).Unix()), 17 * time.Minute, true},
		{"429 seconds", 429, "", "900", "", 15 * time.Minute, true},
		{"secondary 403 date", 403, "8", base.Add(20 * time.Minute).Format(http.TimeFormat), "", 20 * time.Minute, true},
		{"both use later reset", 429, "0", "600", fmt.Sprint(base.Add(25 * time.Minute).Unix()), 25 * time.Minute, true},
		{"both use later retry", 403, "0", "1800", fmt.Sprint(base.Add(5 * time.Minute).Unix()), 30 * time.Minute, true},
		{"unexhausted reset ignored", 403, "8", "420", fmt.Sprint(base.Add(time.Hour).Unix()), 7 * time.Minute, true},
		{"malformed headers", 429, "0", "invalid", "invalid", time.Minute, true},
		{"past headers", 429, "0", base.Add(-time.Hour).Format(http.TimeFormat), fmt.Sprint(base.Add(-time.Hour).Unix()), time.Minute, true},
		{"zero delay", 429, "", "0", "", time.Minute, true},
		{"overflow delay", 429, "", "9223372036854775807", "", time.Minute, true},
		{"successful exhausted quota without reset", 200, "0", "", "", time.Minute, false},
		{"successful last quota request", 200, "0", "", fmt.Sprint(base.Add(13 * time.Minute).Unix()), 13 * time.Minute, false},
		{"304 last quota request", 304, "0", "", fmt.Sprint(base.Add(13 * time.Minute).Unix()), 13 * time.Minute, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := fixture(t, "v0.6.2", "darwin", "arm64")
			var checks atomic.Int32
			u, log := testUpdater(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("ETag", `"validated"`)
				if checks.Add(1) == 2 {
					w.Header().Set("X-RateLimit-Remaining", test.remaining)
					w.Header().Set("X-RateLimit-Reset", test.reset)
					w.Header().Set("Retry-After", test.retryAfter)
					if test.status != http.StatusOK {
						w.WriteHeader(test.status)
						return
					}
				}
				f.serve(w, r)
			})
			now := base
			u.now = func() time.Time { return now }
			requireCheck(t, u)
			if err := u.Check(context.Background()); (err != nil) != test.limited {
				t.Fatalf("unexpected rate-limit response: %v", err)
			}
			last := u.Snapshot().LastChecked
			now = base.Add(test.delay - time.Nanosecond)
			// Neither manual checks nor automatic ticks may bypass server backoff.
			for _, automatic := range []bool{false, true} {
				if err := u.checkForUpdates(context.Background(), automatic); err == nil || !strings.Contains(err.Error(), "retry after") {
					t.Fatalf("backoff was not explained: %v", err)
				}
			}
			if log.count() != 2 || !u.Snapshot().Available || !u.Snapshot().LastChecked.Equal(last) || u.Snapshot().Checking {
				t.Fatalf("backoff made requests or changed the offer/check time: %+v, requests=%d", u.Snapshot(), log.count())
			}
			now = base.Add(test.delay)
			requireCheck(t, u)
			if log.count() != 3 || u.Snapshot().Error != "" || !u.Snapshot().LastChecked.Equal(now) {
				t.Fatal("check did not resume when server backoff expired")
			}
		})
	}
}

func TestDiscoveryDoesNotResetRepeatedAPILimitBackoff(t *testing.T) {
	f := fixture(t, "v0.6.2", "darwin", "arm64")
	var api atomic.Int32
	u, log := testUpdater(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/idy/tailsplit/releases/latest" && api.Add(1) > 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		f.serve(w, r)
	})
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	u.now = func() time.Time { return now }
	requireCheck(t, u)
	f = fixture(t, "v0.6.3", "darwin", "arm64")
	for i, delay := range []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute} {
		if err := u.checkForUpdates(context.Background(), true); err == nil {
			t.Fatal("accepted secondary API limit after a successful web probe")
		}
		now = now.Add(delay - time.Nanosecond)
		if err := u.checkForUpdates(context.Background(), true); err == nil || log.count() != 1+2*(i+1) {
			t.Fatal("successful web discovery reset the API's repeated-limit backoff")
		}
		now = now.Add(time.Nanosecond)
	}
}

func TestSecondaryLimitFallbackIncreasesAndOtherHeadersDoNotThrottle(t *testing.T) {
	f := fixture(t, "v0.6.2", "darwin", "arm64")
	u, log := testUpdater(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	u.now = func() time.Time { return now }
	for i, delay := range []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute} {
		if err := u.Check(context.Background()); err == nil {
			t.Fatal("accepted secondary limit")
		}
		now = now.Add(delay - time.Nanosecond)
		if err := u.Check(context.Background()); err == nil || log.count() != i+1 {
			t.Fatal("repeated secondary limit did not increase the fallback")
		}
		now = now.Add(time.Nanosecond)
	}
	for _, status := range []int{http.StatusForbidden, http.StatusOK} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var checks atomic.Int32
			u, log := testUpdater(t, func(w http.ResponseWriter, r *http.Request) {
				if checks.Add(1) == 1 {
					w.Header().Set("X-RateLimit-Remaining", "8")
					w.Header().Set("X-RateLimit-Reset", fmt.Sprint(now.Add(time.Hour).Unix()))
					if status != http.StatusOK {
						w.WriteHeader(status)
						return
					}
				}
				f.serve(w, r)
			})
			u.now = func() time.Time { return now }
			_ = u.Check(context.Background())
			requireCheck(t, u)
			if log.count() != 2 {
				t.Fatal("unrelated status or unexhausted quota caused backoff")
			}
		})
	}
}
