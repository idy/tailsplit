package configweb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type peettyTransport func(*http.Request) (*http.Response, error)

func (f peettyTransport) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

type peettyCDNFixture struct {
	versions  []string
	files     map[string]map[string][]byte
	publisher map[string]map[string][]byte
	annotated bool
	tagLoop   bool
	fail      string
	tamper    string
	requests  []string
}

func peettyTestFiles(color string, notices bool) map[string][]byte {
	files := map[string][]byte{
		"cdn/peetty.global.js": []byte("var css=`\n:host { all: initial; }\n.pet { color: " + color + "; }\n`;document.createElement(\"style\");function createPeetty(){document.createElement(\"peetty-companion\")};"),
		"cdn/LICENSE":          []byte("MIT License"),
	}
	if notices {
		files["cdn/THIRD_PARTY_NOTICES.txt"] = []byte("Dependency notices")
	}
	return files
}

func peettyTestCommit(version string) string {
	digest := sha256.Sum256([]byte(version))
	return fmt.Sprintf("%x", digest[:20])
}

func (cdn *peettyCDNFixture) attach(t *testing.T, updater *peettyUpdates) {
	updater.client = cdn.client(t)
	updater.sourceClient = updater.client
}

func (cdn *peettyCDNFixture) client(t *testing.T) *http.Client {
	t.Helper()
	return &http.Client{Transport: peettyTransport(func(request *http.Request) (*http.Response, error) {
		if err := request.Context().Err(); err != nil {
			return nil, err
		}
		source := request.URL.String()
		cdn.requests = append(cdn.requests, source)
		status := http.StatusOK
		var body []byte
		switch {
		case cdn.fail != "" && strings.Contains(source, cdn.fail):
			status = http.StatusServiceUnavailable
		case source == peettyVersionsURL:
			body, _ = json.Marshal(map[string]any{"versions": cdn.versions})
		case strings.HasPrefix(source, peettyGitHubAPI+"/git/"):
			publisher := cdn.publisher
			if publisher == nil {
				publisher = cdn.files
			}
			var version string
			if strings.Contains(source, "/git/ref/tags/") {
				version = strings.TrimPrefix(source, peettyGitHubAPI+"/git/ref/tags/")
			} else {
				for candidate := range publisher {
					if strings.TrimPrefix(source, peettyGitHubAPI+"/git/tags/") == peettyTestCommit("tag-"+candidate) {
						version = candidate
					}
				}
			}
			kind, sha := "commit", peettyTestCommit(version)
			if cdn.tagLoop || cdn.annotated && strings.Contains(source, "/git/ref/tags/") {
				kind, sha = "tag", peettyTestCommit("tag-"+version)
			}
			if publisher[version] == nil {
				status = http.StatusNotFound
			}
			body, _ = json.Marshal(map[string]any{"object": map[string]any{"type": kind, "sha": sha}})
		case strings.HasPrefix(source, peettySourceBase):
			commit, name, _ := strings.Cut(strings.TrimPrefix(source, peettySourceBase), "/")
			publisher := cdn.publisher
			if publisher == nil {
				publisher = cdn.files
			}
			for version, files := range publisher {
				if peettyTestCommit(version) == commit {
					body = files[name]
				}
			}
			if len(body) == 0 {
				status = http.StatusNotFound
			}
		case strings.HasPrefix(source, peettyCDNBase):
			commit, name, _ := strings.Cut(strings.TrimPrefix(source, peettyCDNBase), "/")
			for version, files := range cdn.files {
				if peettyTestCommit(version) == commit {
					body = files[name]
				}
			}
			if name == cdn.tamper {
				body = append(bytes.Clone(body), '!')
			}
			if len(body) == 0 {
				status = http.StatusNotFound
			}
		default:
			t.Errorf("unexpected outbound request %s", source)
			status = http.StatusBadRequest
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), Request: request}, nil
	})}
}

func TestPeettyUpdatesPinStableAssetsAndRestoreOfflineCache(t *testing.T) {
	config := filepath.Join(t.TempDir(), "config.yaml")
	cdn := &peettyCDNFixture{
		versions: []string{"0.3.1", "0.3.2", "0.3.3-beta.1", "0.4.0", "1.0.0", "../../main"},
		files:    map[string]map[string][]byte{"v0.3.2": peettyTestFiles("red", true)},
	}
	u := newPeettyUpdates(config)
	cdn.attach(t, u)
	if err := u.check(context.Background()); err != nil {
		t.Fatal(err)
	}
	release := u.snapshot()
	if release == nil || release.Version != "v0.3.2" || len(release.Files) != 3 {
		t.Fatalf("wrong release: %+v", release)
	}
	style := "\n:host { all: initial; }\n.pet { color: red; }\n"
	digest := sha256.Sum256([]byte(style))
	if release.style != "'sha256-"+base64.StdEncoding.EncodeToString(digest[:])+"'" {
		t.Fatalf("stylesheet hash does not include exact literal: %s", release.style)
	}
	if err := u.check(context.Background()); err != nil || len(cdn.requests) != 9 {
		t.Fatalf("unchanged release re-downloaded resources: %v, %v", cdn.requests, err)
	}
	cached := newPeettyUpdates(config).snapshot()
	if cached == nil || cached.Version != release.Version || cached.style != release.style || !bytes.Equal(cached.Files["cdn/peetty.global.js"], release.Files["cdn/peetty.global.js"]) {
		t.Fatal("restart did not restore the verified offline cache")
	}
}

func TestPeettyFailedUpdatesKeepActiveAndPersistedVersion(t *testing.T) {
	for _, failure := range []string{"offline", "checksum", "missing script", "changed stylesheet", "disk"} {
		t.Run(failure, func(t *testing.T) {
			config := filepath.Join(t.TempDir(), "config.yaml")
			cdn := &peettyCDNFixture{versions: []string{"0.3.1"}, files: map[string]map[string][]byte{"v0.3.1": peettyTestFiles("red", false)}}
			u := newPeettyUpdates(config)
			cdn.attach(t, u)
			if err := u.check(context.Background()); err != nil {
				t.Fatal(err)
			}
			original, err := os.ReadFile(u.path)
			if err != nil {
				t.Fatal(err)
			}
			cdn.versions = []string{"0.3.2"}
			cdn.files["v0.3.2"] = peettyTestFiles("blue", true)
			switch failure {
			case "offline":
				cdn.fail = "cdn.jsdelivr.net"
			case "checksum":
				cdn.tamper = "cdn/peetty.global.js"
			case "missing script":
				delete(cdn.files["v0.3.2"], "cdn/peetty.global.js")
			case "changed stylesheet":
				cdn.files["v0.3.2"]["cdn/peetty.global.js"] = []byte("var changed=true")
			case "disk":
				u.path = filepath.Join(u.path, "invalid.json")
			}
			if err := u.check(context.Background()); err == nil {
				t.Fatal("invalid update was accepted")
			}
			if u.snapshot().Version != "v0.3.1" {
				t.Fatal("failed update replaced active assets")
			}
			path := newPeettyUpdates(config).path
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(original, after) {
				t.Fatal("failed update damaged the offline cache:", err)
			}
		})
	}
}

func TestPeettyPageAndOldAssetsKeepSameReleaseDuringUpdate(t *testing.T) {
	server, config, _ := testServer(t)
	cdn := &peettyCDNFixture{versions: []string{"0.3.1"}, files: map[string]map[string][]byte{"v0.3.1": peettyTestFiles("red", false)}}
	u := newPeettyUpdates(config)
	cdn.attach(t, u)
	server.peetty = u
	if err := u.check(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := u.snapshot()
	response, err := http.Get("http://" + server.address + "/")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	response.Body.Close()
	policy := response.Header.Get("Content-Security-Policy")
	if !bytes.Contains(page, []byte(first.scriptTag())) || !strings.Contains(policy, first.style) || strings.Contains(policy, "unsafe-") || strings.Contains(policy, "https:") {
		t.Fatal("page and restrictive CSP do not select the same release:", policy)
	}
	cdn.versions = []string{"0.3.2"}
	cdn.files["v0.3.2"] = peettyTestFiles("blue", true)
	if err := u.check(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"v0.3.1", "v0.3.2"} {
		status, body := request(t, server, "GET", peettyAssetBase+version+"/peetty.global.js", nil, nil)
		if status != http.StatusOK || !bytes.Equal(body, peettyBrowserBundle(cdn.files[version]["cdn/peetty.global.js"], "Peetty")) {
			t.Fatalf("open page lost its immutable %s assets: HTTP %d", version, status)
		}
		if version == first.Version {
			digest := sha256.Sum256(body)
			if !bytes.Contains(page, []byte(`integrity="sha256-`+base64.StdEncoding.EncodeToString(digest[:])+`"`)) {
				t.Fatal("page integrity does not cover the served SDK facade")
			}
		}
	}
	for _, path := range []string{
		peettyAssetBase + "v0.3.1/THIRD_PARTY_NOTICES.txt", peettyAssetBase + "v0.3.2/src/pet/ui.ts",
		peettyAssetBase + "v0.3.2/chat/panel.js", peettyAssetBase + "v0.3.2/current.json",
		peettyAssetBase + "v0.3.3/peetty.global.js",
	} {
		status, _ := request(t, server, "GET", path, nil, nil)
		if status != http.StatusNotFound {
			t.Errorf("unexpected public resource %s: HTTP %d", path, status)
		}
	}
}

func TestPeettyCorruptCacheIsIgnoredAndCanceledChecksDoNotPublish(t *testing.T) {
	config := filepath.Join(t.TempDir(), "config.yaml")
	cdn := &peettyCDNFixture{versions: []string{"0.3.1"}, files: map[string]map[string][]byte{"v0.3.1": peettyTestFiles("red", false)}}
	u := newPeettyUpdates(config)
	cdn.attach(t, u)
	if err := u.check(context.Background()); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(u.path)
	var release peettyRelease
	if err := json.Unmarshal(data, &release); err != nil {
		t.Fatal(err)
	}
	release.Files["cdn/peetty.global.js"] = append(release.Files["cdn/peetty.global.js"], '!')
	corrupt, _ := json.Marshal(release)
	if err := os.WriteFile(u.path, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	if newPeettyUpdates(config).snapshot() != nil {
		t.Fatal("corrupted cached executable was accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := u.check(ctx); err == nil {
		t.Fatal("canceled update succeeded")
	}
	if u.snapshot().Version != "v0.3.1" {
		t.Fatal("cancellation changed the active release")
	}
	if _, err := readPeettyBounded(strings.NewReader("oversized"), 2); err == nil {
		t.Fatal("resource limit ignored")
	}
}

func TestPeettyDoesNotLoadOrReplaceCacheThroughDirectorySymlink(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "original.yaml")
	cdn := &peettyCDNFixture{versions: []string{"0.3.1"}, files: map[string]map[string][]byte{"v0.3.1": peettyTestFiles("red", false)}}
	u := newPeettyUpdates(config)
	cdn.attach(t, u)
	if err := u.check(context.Background()); err != nil {
		t.Fatal(err)
	}
	otherConfig := filepath.Join(root, "other.yaml")
	if err := os.Symlink(filepath.Dir(u.path), otherConfig+".peetty"); err != nil {
		t.Skip("directory symlinks unavailable:", err)
	}
	other := newPeettyUpdates(otherConfig)
	if other.snapshot() != nil {
		t.Fatal("cache was loaded through an unsafe directory alias")
	}
	cdn.attach(t, other)
	if err := other.check(context.Background()); err == nil {
		t.Fatal("cache was written through an unsafe directory alias")
	}
	if newPeettyUpdates(config).snapshot() == nil {
		t.Fatal("rejecting the alias damaged the original cache")
	}
}

func TestPeettySupportedDOMConstructionSurface(t *testing.T) {
	for _, test := range []struct {
		code    string
		allowed bool
	}{
		{`document.createElement("div");document.createElementNS(namespace,"svg")`, true},
		{`window.document.createElement(tag)`, false},
		{`var make=document.createElement;make(tag)`, false},
		{`var make=ownerDocument["createElement"];make(tag)`, false},
		{`document.createElement("div").cloneNode(true)`, false},
		{`document.createElement("div");target.innerHTML=markup`, false},
		{`document.createElement("div");frame.srcdoc=markup`, false},
		{`document.createElement("div");new DOMParser().parseFromString(markup,"text/html")`, false},
	} {
		if allowed := supportedPeettyDOMConstruction([]byte(test.code)); allowed != test.allowed {
			t.Errorf("construction support = %v, want %v: %s", allowed, test.allowed, test.code)
		}
	}
}

func TestPeettyRejectsUntrackedHostCreation(t *testing.T) {
	for _, replacement := range []string{
		`window.document.createElement("peetty-companion")`,
		`globalThis.document.createElement("peetty-companion")`,
		`document.createElement("peetty-companion");window.document.createElement("peetty-companion")`,
		`document.createElement("peetty-companion");globalThis.document.createElement("peetty-companion")`,
		`document.createElement("peetty-companion");window.document.createElement('peetty-companion')`,
		`document.createElement("peetty-companion");var tag="peetty-companion";window.document.createElement(tag)`,
		`document.createElement("peetty-companion").cloneNode(true)`,
		`var template=document.createElement("peetty-companion");template.cloneNode(true)`,
		`var template=document.createElement("peetty-companion");template["cloneNode"](true)`,
		`var template=document.createElement("peetty-companion");document.importNode(template,true)`,
		`document.createElement("peetty-companion");window.document.createElement(tag)`,
		`document.createElement("peetty-companion");var make=window.document["createElement"];make(tag)`,
		`document.createElement("peetty-companion");var fragment=document.createRange().createContextualFragment(html)`,
		`document.createElement("peetty-companion");target.innerHTML=html`,
		`document.createElement("peetty-companion");document.write(html)`,
		`document.createElement("peetty-companion");new DOMParser().parseFromString(html,"text/html")`,
	} {
		release := &peettyRelease{Format: peettyCacheFormat, Commit: peettyTestCommit("v0.3.1"), Version: "v0.3.1", Files: peettyTestFiles("red", false), Hashes: make(map[string]string)}
		release.Files["cdn/peetty.global.js"] = bytes.ReplaceAll(release.Files["cdn/peetty.global.js"], []byte(`document.createElement("peetty-companion")`), []byte(replacement))
		for name, body := range release.Files {
			digest := sha256.Sum256(body)
			release.Hashes[name] = base64.StdEncoding.EncodeToString(digest[:])
		}
		if err := preparePeettyRelease(release); err == nil {
			t.Fatalf("host creation bypassing the document facade was accepted: %s", replacement)
		}
	}
}

func TestPeettyRejectsUnrecognizedOrDynamicStyles(t *testing.T) {
	for _, css := range []string{"${dynamic}", `\n`, "`"} {
		t.Run(fmt.Sprintf("%q", css), func(t *testing.T) {
			release := &peettyRelease{Format: peettyCacheFormat, Commit: peettyTestCommit("v0.3.1"), Version: "v0.3.1", Files: peettyTestFiles(css, false), Hashes: make(map[string]string)}
			for name, body := range release.Files {
				digest := sha256.Sum256(body)
				release.Hashes[name] = base64.StdEncoding.EncodeToString(digest[:])
			}
			if err := preparePeettyRelease(release); err == nil {
				t.Fatal("stylesheet with unknown runtime interpretation was accepted")
			}
		})
	}
}
