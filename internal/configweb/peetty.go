package configweb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/idy/tailsplit/internal/privatefs"
	"golang.org/x/mod/semver"
)

const (
	peettyVersionsURL = "https://data.jsdelivr.com/v1/package/gh/idy/peetty"
	peettyCDNBase     = "https://cdn.jsdelivr.net/gh/idy/peetty@"
	peettyAssetBase   = "/vendor/peetty/releases/"
	peettyCacheLimit  = 8 << 20
	peettyFileLimit   = 2 << 20
)

// A release is immutable once published to a page. Keeping old releases in
// memory lets an open page finish loading its own version during an update.
// Only the latest complete release is persisted; the embedded build is always
// available independently of the cache and the network.
type peettyRelease struct {
	Format  int               `json:"format"`
	Commit  string            `json:"commit"`
	Version string            `json:"version"`
	Files   map[string][]byte `json:"files"`
	Hashes  map[string]string `json:"hashes"`
	style   string
}

var peettyStylesheet = regexp.MustCompile("(?s)(?:var|let|const)\\s+[A-Za-z_$][A-Za-z0-9_$]*\\s*=\\s*`(\\n:host \\{ all: initial; \\}[^`]*\\n)`[;,]")

type peettyUpdates struct {
	mu           sync.RWMutex
	active       *peettyRelease
	releases     map[string]*peettyRelease
	path         string
	client       *http.Client
	sourceClient *http.Client
}

func newPeettyUpdates(configPath string) *peettyUpdates {
	parent, err := filepath.EvalSymlinks(filepath.Dir(configPath))
	if err != nil {
		parent = filepath.Dir(configPath)
	}
	u := &peettyUpdates{
		path: filepath.Join(parent, filepath.Base(configPath)+".peetty", "current.json"),
		client: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("Peetty CDN redirects are not allowed")
		}},
		releases:     make(map[string]*peettyRelease),
		sourceClient: newPeettySourceClient(),
	}
	if privatefs.EnsureDir(filepath.Dir(u.path)) == nil && privatefs.CheckFile(u.path) == nil {
		if data, err := readPeettyFile(u.path, peettyCacheLimit); err == nil {
			var release peettyRelease
			if json.Unmarshal(data, &release) == nil && preparePeettyRelease(&release) == nil {
				u.active = &release
				u.releases[release.Version] = &release
			}
		}
	}
	return u
}

func (u *peettyUpdates) snapshot() *peettyRelease {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.active
}

func (u *peettyUpdates) run(ctx context.Context) {
	for {
		if err := u.check(ctx); err != nil && ctx.Err() == nil {
			log.Printf("Peetty update skipped; keeping local assets: %v", err)
		}
		timer := time.NewTimer(6 * time.Hour)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// Only compatible stable 0.3 releases are selected. A new pre-1.0 minor may
// change the SDK contract and needs an integration update before opting in.
func compatiblePeettyVersion(version string) bool {
	return semver.IsValid(version) && semver.Canonical(version) == version &&
		semver.Prerelease(version) == "" && semver.Build(version) == "" &&
		semver.MajorMinor(version) == "v0.3" && semver.Compare(version, "v0.3.1") >= 0
}

func (u *peettyUpdates) check(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	data, err := u.fetch(ctx, peettyVersionsURL, 256<<10)
	if err != nil {
		return err
	}
	var metadata struct {
		Versions []string `json:"versions"`
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		return fmt.Errorf("decode Peetty versions: %w", err)
	}
	version := ""
	for _, tag := range metadata.Versions {
		if !strings.HasPrefix(tag, "v") {
			tag = "v" + tag
		}
		if compatiblePeettyVersion(tag) && (version == "" || semver.Compare(tag, version) > 0) {
			version = tag
		}
	}
	if version == "" {
		return errors.New("no compatible stable Peetty CDN release")
	}
	if current := u.snapshot(); current != nil && semver.Compare(version, current.Version) <= 0 {
		return nil
	}
	release, err := u.downloadRelease(ctx, version)
	if err != nil {
		return err
	}
	if err := preparePeettyRelease(release); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := u.save(release); err != nil {
		return fmt.Errorf("cache Peetty: %w", err)
	}
	u.mu.Lock()
	u.releases[version] = release
	u.active = release
	u.mu.Unlock()
	log.Printf("Peetty %s cached; new configuration pages will use it", version)
	return nil
}

func (u *peettyUpdates) fetch(ctx context.Context, source string, limit int64) ([]byte, error) {
	if !peettyAllowedSource(source) {
		return nil, errors.New("unexpected Peetty resource source")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "TailSplit-Peetty-Updater")
	client := u.client
	if strings.HasPrefix(source, peettyGitHubAPI+"/") || strings.HasPrefix(source, peettySourceBase) {
		client = u.sourceClient
		if client == nil {
			return nil, errors.New("independent Peetty source verifier is unavailable")
		}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, peettyHTTPStatus(response.StatusCode)
	}
	return readPeettyBounded(response.Body, limit)
}

// Hash the exact static overlay stylesheet in the browser bundle. This permits
// that stylesheet without unsafe-inline, upstream source downloads or rewrites
// of minified JavaScript. Unknown build formats keep the last usable release.
func preparePeettyRelease(release *peettyRelease) error {
	// Format 3 requires publisher verification anchored in the application's
	// fixed CA bundle. Caches established using system TLS trust are rejected. Offline reuse
	// trusts the owner-only cache, as it does other local application state.
	if release.Format != peettyCacheFormat || !peettyCommitID(release.Commit) || !compatiblePeettyVersion(release.Version) || len(release.Files) < 2 || len(release.Files) > 3 || len(release.Hashes) != len(release.Files) {
		return errors.New("invalid Peetty release")
	}
	for _, name := range []string{"cdn/peetty.global.js", "cdn/LICENSE"} {
		if len(release.Files[name]) == 0 {
			return fmt.Errorf("missing Peetty asset %s", name)
		}
	}
	for name, body := range release.Files {
		if name != "cdn/peetty.global.js" && name != "cdn/LICENSE" && name != "cdn/THIRD_PARTY_NOTICES.txt" || len(body) == 0 || len(body) > peettyFileLimit {
			return fmt.Errorf("invalid Peetty asset %s", name)
		}
		digest := sha256.Sum256(body)
		if base64.StdEncoding.EncodeToString(digest[:]) != release.Hashes[name] {
			return fmt.Errorf("invalid cached Peetty checksum: %s", name)
		}
	}
	bundle := release.Files["cdn/peetty.global.js"]
	styles := peettyStylesheet.FindAllSubmatch(bundle, -1)
	if len(styles) != 1 || bytes.Contains(styles[0][1], []byte("${")) || bytes.ContainsAny(styles[0][1], "\\") ||
		!bytes.Contains(bundle, []byte("createPeetty")) || !bytes.Contains(bundle, []byte("peetty-companion")) {
		return errors.New("unsupported Peetty browser bundle or stylesheet")
	}
	digest := sha256.Sum256(styles[0][1])
	release.style = "'sha256-" + base64.StdEncoding.EncodeToString(digest[:]) + "'"
	return nil
}

func (u *peettyUpdates) save(release *peettyRelease) error {
	directory := filepath.Dir(u.path)
	if err := privatefs.EnsureDir(directory); err != nil {
		return err
	}
	if _, err := os.Lstat(u.path); err == nil {
		if err := privatefs.CheckFile(u.path); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := json.Marshal(release)
	if err != nil {
		return err
	}
	if len(data) > peettyCacheLimit {
		return errors.New("Peetty cache exceeds size limit")
	}
	file, err := os.CreateTemp(directory, ".peetty-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return privatefs.Replace(file.Name(), u.path)
}

func readPeettyFile(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return readPeettyBounded(file, limit)
}

func readPeettyBounded(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err == nil && int64(len(data)) > limit {
		err = errors.New("Peetty resource exceeds size limit")
	}
	return data, err
}

func (release *peettyRelease) scriptTag() string {
	digest := sha256.Sum256(release.browserScript())
	return `<script src="` + peettyAssetBase + release.Version + `/peetty.global.js" data-peetty-release="` + release.Version +
		`" integrity="sha256-` + base64.StdEncoding.EncodeToString(digest[:]) + `" defer></script>`
}

func (u *peettyUpdates) serveAsset(w http.ResponseWriter, r *http.Request) {
	version, name, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, peettyAssetBase), "/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	u.mu.RLock()
	release := u.releases[version]
	u.mu.RUnlock()
	if release == nil || name != "peetty.global.js" && name != "LICENSE" && name != "THIRD_PARTY_NOTICES.txt" || len(release.Files["cdn/"+name]) == 0 {
		http.NotFound(w, r)
		return
	}
	body := release.Files["cdn/"+name]
	if name == "peetty.global.js" {
		body = release.browserScript()
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	} else {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	w.Header().Set("X-Peetty-Version", version)
	_, _ = w.Write(body)
}
