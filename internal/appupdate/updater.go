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
	latestReleaseURL = "https://api.github.com/repos/idy/tailsplit/releases/latest"
	releasePageBase  = "https://github.com/idy/tailsplit/releases/tag/"
	assetURLBase     = "https://github.com/idy/tailsplit/releases/download/"

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
		checkInterval:   6 * time.Hour,
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
// six-hour intervals. Disabled and development builds skip timed checks. The
// preference does not reset that cadence. Only one Run loop can be active.
// Automatic checks only discover updates; they never download or install them.
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
	for {
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-u.after(delay):
		}
		u.mu.Lock()
		enabled := u.state.Automatic && u.current != ""
		u.mu.Unlock()
		if enabled {
			_ = u.Check(ctx)
		}
		delay = u.checkInterval
	}
}

// Check discovers a strictly newer stable desktop release. A failed recheck
// retains any previous offer so temporary network failures do not lose it.
func (u *Updater) Check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	u.mu.Lock()
	if u.state.Checking || u.state.Downloading {
		u.mu.Unlock()
		return ErrBusy
	}
	u.state.Checking = true
	u.state.Error = ""
	u.mu.Unlock()

	result, err := u.check(ctx)
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

func (u *Updater) check(ctx context.Context) (checkResult, error) {
	if u.current == "" {
		return checkResult{}, errors.New("updates require a complete stable current version")
	}
	release, err := u.fetchRelease(ctx)
	if err != nil {
		return checkResult{}, err
	}
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
