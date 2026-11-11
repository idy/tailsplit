package appupdate

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/mod/semver"
)

const (
	latestReleaseURL  = "https://api.github.com/repos/idy/tailsplit/releases/latest"
	latestReleasePage = "https://github.com/idy/tailsplit/releases/latest"
	releasePageBase   = "https://github.com/idy/tailsplit/releases/tag/"
	assetURLBase      = "https://github.com/idy/tailsplit/releases/download/"

	// Incomplete releases may take time to finish publishing their assets. Keep
	// probing tags every five minutes, but retry invalid metadata less often.
	metadataRetryDelay = 15 * time.Minute

	maxJSONBytes    = 1 << 20
	maxSumsBytes    = 1 << 20
	maxNotesBytes   = 64 << 10
	maxPackageBytes = 512 << 20
)

// Updater is safe for concurrent use. Check and Download exclude one another;
// overlapping calls return ErrBusy without changing the active operation.
// Construct an Updater with New rather than using its zero value.
type Updater struct {
	mu      sync.Mutex
	state   State
	offer   *offeredRelease
	running bool
	// issued holds the private directories this Updater created. The startup
	// sweep removes only older ones, never a package still being installed.
	issued map[string]bool

	// cached and failedMetadata belong to the exclusive check operation. Only
	// fully validated releases enter cached. Rate-limit state is protected by mu.
	cached         *cachedRelease
	failedMetadata *metadataFailure
	retryAt        time.Time
	retryDelay     time.Duration

	current         string
	goos            string
	goarch          string
	source          string
	client          *http.Client
	now             func() time.Time
	after           func(time.Duration) <-chan time.Time
	startupDelay    time.Duration
	checkInterval   time.Duration
	requestTimeout  time.Duration
	downloadTimeout time.Duration
	packageLimit    int64
	tempRoot        string
}

type releaseAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

type githubRelease struct {
	Tag        string         `json:"tag_name"`
	URL        string         `json:"html_url"`
	Notes      string         `json:"body"`
	Draft      bool           `json:"draft"`
	Prerelease bool           `json:"prerelease"`
	Assets     []releaseAsset `json:"assets"`
}

type offeredRelease struct {
	version string
	asset   releaseAsset
	sums    releaseAsset
}

type checkResult struct {
	version string
	url     string
	notes   string
	offer   *offeredRelease
}

type cachedRelease struct {
	tag    string
	etag   string
	result checkResult
}

// metadataFailure is only a retry hint, never a release or download offer.
// An empty tag means validation failed before a stable release page was known.
type metadataFailure struct {
	tag     string
	retryAt time.Time
	err     error
}

// New configures the public idy/tailsplit release source and desktop platform.
// CurrentVersion retains version as supplied; discovered versions omit "v".
// Development, prerelease, and incomplete versions cannot be checked.
func New(version string, automatic bool) *Updater {
	current, _ := stableVersion(version)
	return &Updater{
		state:           State{CurrentVersion: version, Automatic: automatic},
		current:         current,
		goos:            runtime.GOOS,
		goarch:          runtime.GOARCH,
		source:          latestReleaseURL,
		client:          &http.Client{},
		now:             time.Now,
		after:           time.After,
		startupDelay:    10 * time.Second,
		checkInterval:   5 * time.Minute,
		requestTimeout:  30 * time.Second,
		downloadTimeout: 15 * time.Minute,
		packageLimit:    maxPackageBytes,
		tempRoot:        os.TempDir(),
	}
}

// Snapshot returns an independent copy without waiting for network operations.
func (u *Updater) Snapshot() State {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.state
}

// SetAutomatic changes the in-memory scheduling preference. The caller owns
// persistence. It does not interrupt a manual or already running operation.
func (u *Updater) SetAutomatic(automatic bool) {
	u.mu.Lock()
	u.state.Automatic = automatic
	u.mu.Unlock()
}

// Run waits for the lifecycle context, checking after ten seconds and then at
// five-minute intervals. Disabled and development builds skip timed checks. The
// preference does not reset that cadence. Only one Run loop can be active.
// Automatic checks only discover updates; they never download or install them.
// After a metadata attempt, checks use the public latest-release redirect.
// Valid unchanged tags reuse the cache; invalid metadata for the same tag is
// retried after fifteen minutes while public probes continue every five minutes.
// New tags bypass that validation delay. Use Check to refresh same-tag edits.
// After the startup delay, Run also removes downloads left by earlier processes.
func (u *Updater) Run(ctx context.Context) {
	u.mu.Lock()
	if u.running {
		u.mu.Unlock()
		return
	}
	u.running = true
	u.mu.Unlock()
	defer func() {
		u.mu.Lock()
		u.running = false
		u.mu.Unlock()
	}()

	delay := u.startupDelay
	for first := true; ; first = false {
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-u.after(delay):
		}
		if first {
			u.removeStaleDownloads()
		}
		u.mu.Lock()
		enabled := u.state.Automatic && u.current != ""
		u.mu.Unlock()
		if enabled {
			_ = u.checkForUpdates(ctx, true)
		}
		delay = u.checkInterval
	}
}

// Check discovers a strictly newer stable desktop release, even when automatic
// checks are disabled. Manual checks always revalidate API metadata (using its
// ETag when available), including edits to an existing tag. They bypass the
// local validation retry delay. Both manual and timed checks honor server
// backoff without making requests or advancing LastChecked during the wait.
// A failed recheck retains any previous offer.
func (u *Updater) Check(ctx context.Context) error {
	return u.checkForUpdates(ctx, false)
}

func (u *Updater) checkForUpdates(ctx context.Context, automatic bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	u.mu.Lock()
	if u.state.Checking || u.state.Downloading {
		u.mu.Unlock()
		return ErrBusy
	}
	if u.now().Before(u.retryAt) {
		err := rateLimitError(u.retryAt)
		u.state.Error = err.Error()
		u.mu.Unlock()
		return err
	}
	u.state.Checking = true
	u.state.Error = ""
	u.mu.Unlock()

	result, err := u.check(ctx, automatic)
	checked := u.now()
	u.mu.Lock()
	defer u.mu.Unlock()
	u.state.Checking = false
	u.state.LastChecked = checked
	if err != nil {
		u.state.Error = err.Error()
		return err
	}
	u.offer = result.offer
	u.state.LatestVersion = result.version
	u.state.ReleaseURL = result.url
	u.state.ReleaseNotes = result.notes
	u.state.Available = result.offer != nil
	u.state.DownloadedBytes = 0
	u.state.TotalBytes = 0
	u.state.Error = ""
	return nil
}

func (u *Updater) check(ctx context.Context, automatic bool) (checkResult, error) {
	if u.current == "" {
		return checkResult{}, errors.New("updates require a complete stable current version")
	}
	if u.source != latestReleaseURL {
		return checkResult{}, errors.New("update source is not the expected public GitHub release URL")
	}
	var tag string
	if automatic && (u.cached != nil || u.failedMetadata != nil) {
		// Unauthenticated API requests consume quota even on a 304. Probe the
		// public web redirect instead; never follow it or trust it as metadata.
		var err error
		tag, err = u.latestTag(ctx)
		if err != nil {
			return checkResult{}, err
		}
		if u.cached != nil && tag == u.cached.tag {
			return u.cached.result, nil
		}
		if failed := u.failedMetadata; failed != nil && failed.tag == tag && u.now().Before(failed.retryAt) {
			return checkResult{}, fmt.Errorf("release metadata retry deferred until %s: %w", failed.retryAt.UTC().Format(time.RFC3339), failed.err)
		}
	}
	return u.fetchRelease(ctx, tag)
}

func (u *Updater) validateRelease(release githubRelease) (checkResult, error) {
	latest, valid := stableVersion(release.Tag)
	if !valid || release.Draft || release.Prerelease {
		return checkResult{}, errors.New("GitHub did not return a stable release")
	}
	if release.URL != releasePageBase+release.Tag {
		return checkResult{}, errors.New("release page is not the expected idy/tailsplit URL")
	}
	result := checkResult{
		version: strings.TrimPrefix(release.Tag, "v"),
		url:     release.URL,
		notes:   boundedNotes(release.Notes),
	}
	if semver.Compare(latest, u.current) <= 0 {
		return result, nil
	}
	name, err := desktopAssetName(result.version, u.goos, u.goarch)
	if err != nil {
		return checkResult{}, err
	}
	asset, err := findAsset(release, name, u.packageLimit)
	if err != nil {
		return checkResult{}, err
	}
	sums, err := findAsset(release, "SHA256SUMS", maxSumsBytes)
	if err != nil {
		return checkResult{}, err
	}
	result.offer = &offeredRelease{version: result.version, asset: asset, sums: sums}
	return result, nil
}

func stableVersion(version string) (string, bool) {
	version = "v" + strings.TrimPrefix(version, "v")
	core, _, _ := strings.Cut(version, "+")
	if strings.Count(core, ".") != 2 || !semver.IsValid(version) || semver.Prerelease(version) != "" {
		return "", false
	}
	return semver.Canonical(version), true
}

func desktopAssetName(version, goos, goarch string) (string, error) {
	if goarch != "amd64" && goarch != "arm64" {
		return "", fmt.Errorf("desktop updates are unsupported for %s/%s", goos, goarch)
	}
	switch goos {
	case "darwin":
		return fmt.Sprintf("TailSplit_%s_darwin_%s.zip", version, goarch), nil
	case "windows":
		return fmt.Sprintf("TailSplit_%s_windows_%s_setup.exe", version, goarch), nil
	default:
		return "", fmt.Errorf("desktop updates are unsupported for %s/%s", goos, goarch)
	}
}

func findAsset(release githubRelease, name string, limit int64) (releaseAsset, error) {
	var found releaseAsset
	count := 0
	for _, asset := range release.Assets {
		if asset.Name == name {
			found = asset
			count++
		}
	}
	if count != 1 {
		return releaseAsset{}, fmt.Errorf("release must contain exactly one %s asset", name)
	}
	if found.Size <= 0 || found.Size > limit {
		return releaseAsset{}, fmt.Errorf("release asset %s has an invalid or excessive size", name)
	}
	if found.URL != assetURLBase+release.Tag+"/"+name {
		return releaseAsset{}, fmt.Errorf("release asset %s is not the expected idy/tailsplit URL", name)
	}
	return found, nil
}

func boundedNotes(notes string) string {
	if len(notes) <= maxNotesBytes {
		return notes
	}
	notes = notes[:maxNotesBytes]
	for !utf8.ValidString(notes) {
		notes = notes[:len(notes)-1]
	}
	return notes
}
