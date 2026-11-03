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
