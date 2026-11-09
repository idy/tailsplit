package appupdate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/idy/tailsplit/internal/privatefs"
)

func TestVerifiedDownloadAllDesktopPlatforms(t *testing.T) {
	for _, goos := range []string{"darwin", "windows"} {
		for _, goarch := range []string{"amd64", "arm64"} {
			t.Run(goos+"/"+goarch, func(t *testing.T) {
				f := fixture(t, "v0.6.2", goos, goarch)
				u, log := testUpdater(t, f.serve)
				u.goos, u.goarch = goos, goarch
				requireCheck(t, u)
				pkg, err := u.Download(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = pkg.Cleanup() })
				data, err := os.ReadFile(pkg.Path)
				if err != nil || !bytes.Equal(data, f.payload) || pkg.Version != "0.6.2" {
					t.Fatalf("unexpected downloaded package: %+v, data=%q, err=%v", pkg, data, err)
				}
				if filepath.Dir(pkg.Path) != pkg.Directory || !filepath.IsAbs(pkg.Path) ||
					filepath.Ext(pkg.Path) != filepath.Ext(f.release.Assets[0].Name) || filepath.Base(pkg.Path) == f.release.Assets[0].Name {
					t.Fatalf("package is not in a unique private path: %+v", pkg)
				}
				if err := privatefs.EnsureDir(pkg.Directory); err != nil {
					t.Fatalf("directory is not private: %v", err)
				}
				if err := privatefs.CheckFile(pkg.Path); err != nil {
					t.Fatalf("file is not private: %v", err)
				}
				state := u.Snapshot()
				if state.Downloading || state.Checking || state.Error != "" || !state.Available ||
					state.DownloadedBytes != int64(len(f.payload)) || state.TotalBytes != int64(len(f.payload)) || log.count() != 3 {
					t.Fatalf("unexpected completion state: %+v; requests=%d", state, log.count())
				}
				if err := pkg.Cleanup(); err != nil {
					t.Fatal(err)
				}
				if err := pkg.Cleanup(); err != nil {
					t.Fatalf("cleanup is not idempotent: %v", err)
				}
				requireEmptyTemp(t, u)
			})
		}
	}
}

func TestCleanupOnlyRemovesOwnedDirectory(t *testing.T) {
	other := t.TempDir()
	if err := (Package{Directory: other}).Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatal("forged Package removed unowned directory")
	}
	f := fixture(t, "v0.6.2", "darwin", "arm64")
	u, _ := testUpdater(t, f.serve)
	requireCheck(t, u)
	pkg, err := u.Download(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	owned := pkg.Directory
	pkg.Directory = other
	if err := pkg.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(owned); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cleanup did not remove original owned directory")
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatal("mutated Package removed unowned directory")
	}
}

func TestDownloadPinsOfferAndFetchesSumsOnce(t *testing.T) {
	f := fixture(t, "v0.6.2", "darwin", "arm64")
	var latestChecks, sumsRequests atomic.Int32
	u, _ := testUpdater(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/latest") && latestChecks.Add(1) > 1 {
			t.Error("download fetched latest metadata again")
			http.Error(w, "latest changed", http.StatusInternalServerError)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/SHA256SUMS") {
			sumsRequests.Add(1)
		}
		f.serve(w, r)
	})
	requireCheck(t, u)
	pkg, err := u.Download(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer pkg.Cleanup()
	if latestChecks.Load() != 1 || sumsRequests.Load() != 1 || pkg.Version != "0.6.2" {
		t.Fatalf("release was not pinned: latest=%d sums=%d version=%s", latestChecks.Load(), sumsRequests.Load(), pkg.Version)
	}
}

func TestMalformedOrDuplicateChecksums(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(releaseFixture) []byte
	}{
		{"duplicate selected", func(f releaseFixture) []byte { return append(append([]byte{}, f.sums...), f.sums...) }},
		{"duplicate unrelated", func(f releaseFixture) []byte {
			line := fmt.Sprintf("%x  unrelated.zip\n", sha256.Sum256(nil))
			return []byte(string(f.sums) + line + line)
		}},
		{"malformed trailing line", func(f releaseFixture) []byte { return append(f.sums, []byte("broken\n")...) }},
		{"invalid digest", func(f releaseFixture) []byte { return []byte("z" + string(f.sums[1:])) }},
		{"single space", func(f releaseFixture) []byte { return bytes.Replace(f.sums, []byte("  "), []byte(" "), 1) }},
		{"binary marker", func(f releaseFixture) []byte { return bytes.Replace(f.sums, []byte("  "), []byte(" *"), 1) }},
		{"extra spacing", func(f releaseFixture) []byte { return bytes.Replace(f.sums, []byte("  "), []byte("   "), 1) }},
		{"path prefix", func(f releaseFixture) []byte { return bytes.Replace(f.sums, []byte("  "), []byte("  ./"), 1) }},
		{"blank line", func(f releaseFixture) []byte { return append(f.sums, '\n') }},
		{"nonexact filename", func(f releaseFixture) []byte {
			return bytes.ReplaceAll(f.sums, []byte("TailSplit"), []byte("tailsplit"))
		}},
		{"empty", func(f releaseFixture) []byte { return nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := fixture(t, "v0.6.2", "darwin", "arm64")
			f.sums = test.change(f)
			// The empty manifest still advertises a positive size so the checker
			// accepts metadata and the downloader has to reject the response.
			f.release.Assets[1].Size = int64(max(1, len(f.sums)))
			u, log := testUpdater(t, f.serve)
			requireCheck(t, u)
			pkg, err := u.Download(context.Background())
			if err == nil || pkg.Path != "" || log.count() != 2 {
				t.Fatalf("invalid manifest accepted: pkg=%+v err=%v requests=%d", pkg, err, log.count())
			}
			requireEmptyTemp(t, u)
			if state := u.Snapshot(); !state.Available || state.Error == "" || state.Downloading {
				t.Fatalf("unexpected failure state: %+v", state)
			}
		})
	}
}

func TestConventionalCRLFAndUppercaseChecksums(t *testing.T) {
	f := fixture(t, "v0.6.2", "darwin", "arm64")
	f.sums = []byte(strings.ToUpper(string(f.sums[:64])) + string(f.sums[64:len(f.sums)-1]) + "\r\n")
	f.release.Assets[1].Size = int64(len(f.sums))
	u, _ := testUpdater(t, f.serve)
	requireCheck(t, u)
	pkg, err := u.Download(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer pkg.Cleanup()
}

func TestPackageCorruptionAndSizeBoundsRemoveArtifacts(t *testing.T) {
	for _, kind := range []string{"checksum", "short", "long", "chunked long", "chunked short", "manifest size", "oversized manifest"} {
		t.Run(kind, func(t *testing.T) {
			f := fixture(t, "v0.6.2", "darwin", "arm64")
			switch kind {
			case "checksum":
				f.payload[0] ^= 1
			case "short", "chunked short":
				f.payload = f.payload[:len(f.payload)-1]
			case "long", "chunked long":
				f.payload = append(f.payload, 'x')
			case "manifest size":
				f.release.Assets[1].Size++
			case "oversized manifest":
				f.sums = bytes.Repeat([]byte("a"), maxSumsBytes+1)
			}
			u, _ := testUpdater(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(kind, "chunked") && strings.HasSuffix(r.URL.Path, ".zip") ||
					kind == "oversized manifest" && strings.HasSuffix(r.URL.Path, "/SHA256SUMS") {
					w.(http.Flusher).Flush()
				}
				f.serve(w, r)
			})
			requireCheck(t, u)
			if pkg, err := u.Download(context.Background()); err == nil || pkg.Path != "" {
				t.Fatalf("accepted corrupt/incorrect size package: pkg=%+v err=%v", pkg, err)
			}
			requireEmptyTemp(t, u)
		})
	}
}

func TestTrustedGitHubAssetRedirects(t *testing.T) {
	for _, host := range []string{"release-assets.githubusercontent.com", "objects.githubusercontent.com", "github-releases.githubusercontent.com"} {
		t.Run(host, func(t *testing.T) {
			f := fixture(t, "v0.6.2", "darwin", "arm64")
			u, log := testUpdater(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Host == "github.com" {
					name := filepath.Base(r.URL.Path)
					http.Redirect(w, r, "https://"+host+"/github-production-release-asset/"+name+"?signed=fixture", http.StatusFound)
					return
				}
				if r.Host == host {
					if strings.HasSuffix(r.URL.Path, "/SHA256SUMS") {
						_, _ = w.Write(f.sums)
					} else {
						_, _ = w.Write(f.payload)
					}
					return
				}
				f.serve(w, r)
			})
			requireCheck(t, u)
			pkg, err := u.Download(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer pkg.Cleanup()
			if log.count() != 5 {
				t.Fatalf("unexpected redirect requests: %d", log.count())
			}
		})
	}
}

func TestRejectAssetRedirects(t *testing.T) {
	for _, destination := range []string{
		"http://release-assets.githubusercontent.com/payload",
		"https://evil.example/payload",
		"https://release-assets.githubusercontent.com.evil.example/payload",
		"https://github.com/foreign/tailsplit/releases/download/v0.6.2/SHA256SUMS",
		"https://github.com/idy/tailsplit/releases/download/v0.6.3/SHA256SUMS",
		"https://github.com/idy/tailsplit/releases/download/v0.6.2/other.zip",
		"https://user@objects.githubusercontent.com/payload",
		"https://objects.githubusercontent.com:443/payload",
		"https://objects.githubusercontent.com/payload#fragment",
	} {
		t.Run(destination, func(t *testing.T) {
			f := fixture(t, "v0.6.2", "darwin", "arm64")
			u, log := testUpdater(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Host == "github.com" {
					http.Redirect(w, r, destination, http.StatusFound)
					return
				}
				f.serve(w, r)
			})
			requireCheck(t, u)
			if _, err := u.Download(context.Background()); err == nil || log.count() != 2 {
				t.Fatalf("untrusted redirect was followed: err=%v requests=%d", err, log.count())
			}
			requireEmptyTemp(t, u)
		})
	}
}

func TestRedirectLimitAndSignedURLErrorPrivacy(t *testing.T) {
	f := fixture(t, "v0.6.2", "darwin", "arm64")
	u, log := testUpdater(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "github.com" || r.Host == "objects.githubusercontent.com" {
			http.Redirect(w, r, "https://objects.githubusercontent.com/payload?secret=fixture", http.StatusFound)
			return
		}
		f.serve(w, r)
	})
	requireCheck(t, u)
	_, err := u.Download(context.Background())
	if err == nil || !strings.Contains(err.Error(), "too many") || strings.Contains(err.Error(), "secret=fixture") || log.count() != 6 {
		t.Fatalf("unexpected redirect-limit result: err=%v requests=%d", err, log.count())
	}
}

func TestDownloadCancellationAndTimeoutRemovePartialArtifacts(t *testing.T) {
	for _, stage := range []struct {
		name     string
		manifest bool
		timeout  bool
	}{
		{name: "manifest", manifest: true},
		{name: "package"},
		{name: "package timeout", timeout: true},
		{name: "manifest timeout", manifest: true, timeout: true},
	} {
		t.Run(stage.name, func(t *testing.T) {
			f := fixture(t, "v0.6.2", "darwin", "arm64")
			started := make(chan struct{})
			u, _ := testUpdater(t, func(w http.ResponseWriter, r *http.Request) {
				isSums := strings.HasSuffix(r.URL.Path, "/SHA256SUMS")
				isPackage := strings.HasSuffix(r.URL.Path, ".zip")
				if stage.manifest && isSums || !stage.manifest && isPackage {
					if isPackage {
						_, _ = w.Write(f.payload[:len(f.payload)/2])
						w.(http.Flusher).Flush()
					}
					close(started)
					<-r.Context().Done()
					return
				}
				f.serve(w, r)
			})
			requireCheck(t, u)
			if stage.timeout {
				u.downloadTimeout = 75 * time.Millisecond
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				pkg, err := u.Download(ctx)
				if pkg.Path != "" {
					_ = pkg.Cleanup()
				}
				done <- err
			}()
			// The overall deadline can expire before a package request starts,
			// including during checksum fetching or Windows private-directory setup.
			if !stage.timeout {
				waitSignal(t, started)
				cancel()
			}
			err := waitError(t, done)
			expected := error(context.Canceled)
			if stage.timeout {
				expected = context.DeadlineExceeded
			}
			if !errors.Is(err, expected) || u.Snapshot().Downloading || u.Snapshot().Error == "" {
				t.Fatalf("unexpected canceled download: err=%v state=%+v", err, u.Snapshot())
			}
			requireEmptyTemp(t, u)
		})
	}
}

func TestDownloadHTTPErrorAndCanceledContext(t *testing.T) {
	f := fixture(t, "v0.6.2", "darwin", "arm64")
	u, log := testUpdater(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/SHA256SUMS") {
			http.Error(w, "fixture failure", http.StatusServiceUnavailable)
			return
		}
		f.serve(w, r)
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := u.Check(ctx); !errors.Is(err, context.Canceled) || log.count() != 0 {
		t.Fatalf("pre-canceled check made a request: %v", err)
	}
	requireCheck(t, u)
	if _, err := u.Download(ctx); !errors.Is(err, context.Canceled) || log.count() != 1 {
		t.Fatalf("pre-canceled download made a request: %v", err)
	}
	if _, err := u.Download(context.Background()); err == nil || log.count() != 2 {
		t.Fatalf("manifest HTTP failure was ignored: %v", err)
	}
	requireEmptyTemp(t, u)
}

func requireEmptyTemp(t *testing.T, u *Updater) {
	t.Helper()
	entries, err := os.ReadDir(u.tempRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("partial update artifacts remain: %v", entries)
	}
}
