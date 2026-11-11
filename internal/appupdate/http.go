package appupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (u *Updater) fetchRelease(ctx context.Context, tag string) (result checkResult, err error) {
	ctx, cancel := context.WithTimeout(ctx, u.requestTimeout)
	defer cancel()
	response, err := u.get(ctx, u.source, false)
	if err != nil {
		return checkResult{}, err
	}
	defer response.Body.Close()
	// HTTP/rate-limit failures retain their existing retry behavior. An accepted
	// response that cannot produce validated metadata gets a separate retry
	// deadline, which does not suppress public tag discovery or manual checks.
	defer func() {
		if err == nil {
			u.failedMetadata = nil
		} else {
			u.failedMetadata = &metadataFailure{tag: tag, retryAt: u.now().Add(metadataRetryDelay), err: err}
		}
	}()
	if response.StatusCode == http.StatusNotModified {
		if u.cached == nil || u.cached.etag == "" {
			return checkResult{}, errors.New("GitHub returned 304 without a validated cached release")
		}
		if etag := response.Header.Get("ETag"); etag != "" &&
			(!validETag(etag) || strings.TrimPrefix(etag, "W/") != strings.TrimPrefix(u.cached.etag, "W/")) {
			return checkResult{}, errors.New("GitHub returned 304 with a different or malformed ETag")
		}
		if tag != "" && tag != u.cached.tag {
			return checkResult{}, errors.New("GitHub returned 304 for a different latest release tag")
		}
		return u.cached.result, nil
	}
	data, err := readBounded(response, maxJSONBytes)
	if err != nil {
		return checkResult{}, fmt.Errorf("read release metadata: %w", err)
	}
	var release githubRelease
	if err := json.Unmarshal(data, &release); err != nil {
		return checkResult{}, fmt.Errorf("decode release metadata: %w", err)
	}
	if tag == "" {
		// Initial/manual checks may not have probed the public redirect. Use a
		// stable, repository-pinned page only as a retry hint, never as an offer.
		if _, valid := stableVersion(release.Tag); valid && release.URL == releasePageBase+release.Tag {
			tag = release.Tag
		}
	}
	result, err = u.validateRelease(release)
	if err != nil {
		return checkResult{}, err
	}
	if tag != release.Tag {
		return checkResult{}, errors.New("release metadata does not match the public latest release tag")
	}
	etag := response.Header.Get("ETag")
	if !validETag(etag) {
		etag = ""
	}
	// Do not associate an ETag with unvalidated JSON, release URLs or assets.
	// Keep the validated result even without an ETag for web redirect discovery.
	u.cached = &cachedRelease{tag: release.Tag, etag: etag, result: result}
	return result, nil
}

func validETag(etag string) bool {
	etag = strings.TrimPrefix(etag, "W/")
	if len(etag) < 2 || len(etag) > 1024 || etag[0] != '"' || etag[len(etag)-1] != '"' {
		return false
	}
	for i := 1; i < len(etag)-1; i++ {
		if etag[i] < 0x21 || etag[i] == '"' || etag[i] == 0x7f {
			return false
		}
	}
	return true
}

func (u *Updater) latestTag(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, u.requestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, latestReleasePage, nil)
	if err != nil {
		return "", err
	}
	response, err := u.do(request, false, true)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	location := response.Header.Get("Location")
	tag := strings.TrimPrefix(location, releasePageBase)
	if _, valid := stableVersion(tag); !valid || location != releasePageBase+tag {
		return "", errors.New("latest release redirect is not the expected stable idy/tailsplit URL")
	}
	return tag, nil
}

func (u *Updater) get(ctx context.Context, source string, asset bool) (*http.Response, error) {
	if !asset && source != latestReleaseURL || asset && !trustedAssetSource(source) {
		return nil, errors.New("update source is not the expected public GitHub release URL")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, fmt.Errorf("create update request: %w", err)
	}
	if asset {
		request.Header.Set("Accept", "application/octet-stream")
	} else {
		request.Header.Set("Accept", "application/vnd.github+json")
		request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		if u.cached != nil && u.cached.etag != "" {
			request.Header.Set("If-None-Match", u.cached.etag)
		}
	}
	return u.do(request, asset, false)
}

func (u *Updater) do(request *http.Request, asset, discovery bool) (*http.Response, error) {
	if !asset {
		u.mu.Lock()
		retryAt := u.retryAt
		u.mu.Unlock()
		if u.now().Before(retryAt) {
			return nil, rateLimitError(retryAt)
		}
	}
	request.Header.Set("User-Agent", "TailSplit-Updater")
	client := *u.client
	client.Jar = nil
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if discovery {
			// Inspect only the first Location; never fetch a release page or a
			// redirect target, including an otherwise trusted GitHub host.
			return http.ErrUseLastResponse
		}
		if !asset {
			return errors.New("release metadata redirects are not allowed")
		}
		if len(via) >= 5 {
			return errors.New("too many release asset redirects")
		}
		if !trustedAssetRedirect(next.URL, request.URL.String()) {
			return errors.New("release asset redirect is not a trusted HTTPS GitHub asset URL")
		}
		return nil
	}
	response, err := client.Do(request)
	if err != nil {
		// A signed GitHub asset URL may contain short-lived credentials. Report
		// the underlying failure without including that URL in UI state.
		var requestError *url.Error
		if errors.As(err, &requestError) {
			err = requestError.Err
		}
		return nil, fmt.Errorf("request GitHub release: %w", err)
	}
	if !asset {
		if err := u.observeRateLimit(response, discovery); err != nil {
			response.Body.Close()
			return nil, err
		}
	}
	accepted := response.StatusCode == http.StatusOK || !asset && response.StatusCode == http.StatusNotModified
	if discovery {
		accepted = releaseRedirectStatus(response.StatusCode)
	}
	if !accepted {
		response.Body.Close()
		if response.StatusCode == http.StatusTooManyRequests ||
			response.StatusCode == http.StatusForbidden && response.Header.Get("X-RateLimit-Remaining") == "0" {
			return nil, fmt.Errorf("GitHub rate limit reached (HTTP %d)", response.StatusCode)
		}
		return nil, fmt.Errorf("GitHub release request failed (HTTP %d %s)", response.StatusCode, http.StatusText(response.StatusCode))
	}
	return response, nil
}

func releaseRedirectStatus(status int) bool {
	switch status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	}
	return false
}

func rateLimitError(until time.Time) error {
	return fmt.Errorf("GitHub rate limit reached; retry after %s", until.UTC().Format(time.RFC3339))
}

// observeRateLimit applies only to release checks. Existing verified offers can
// still be downloaded from GitHub's asset hosts while the API quota is exhausted.
func (u *Updater) observeRateLimit(response *http.Response, discovery bool) error {
	now := u.now()
	exhausted := response.Header.Get("X-RateLimit-Remaining") == "0"
	limited := response.StatusCode == http.StatusTooManyRequests || response.StatusCode == http.StatusForbidden &&
		(exhausted || response.Header.Get("Retry-After") != "")
	if !limited && !exhausted {
		// A successful web probe does not establish that the API has recovered
		// from repeated secondary limits. Reset only after metadata succeeds.
		if !discovery && (response.StatusCode == http.StatusOK || response.StatusCode == http.StatusNotModified) {
			u.mu.Lock()
			u.retryDelay = 0
			u.mu.Unlock()
		}
		return nil
	}
	var until time.Time
	if limited {
		value := response.Header.Get("Retry-After")
		if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds > 0 && seconds <= int64((1<<63-1)/time.Second) {
			until = now.Add(time.Duration(seconds) * time.Second)
		} else if date, err := http.ParseTime(value); err == nil && date.After(now) {
			until = date
		}
	}
	// A reset timestamp is meaningful only when the primary quota is exhausted.
	if exhausted {
		if seconds, err := strconv.ParseInt(response.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil && seconds > 0 {
			if reset := time.Unix(seconds, 0); reset.After(now) && reset.After(until) {
				until = reset
			}
		}
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if limited {
		// Secondary limits may omit usable headers. Wait at least a minute,
		// increasing the fallback on repeated limits, without retrying inline.
		if u.retryDelay == 0 {
			u.retryDelay = time.Minute
		} else {
			u.retryDelay = min(2*u.retryDelay, time.Hour)
		}
	}
	if !until.After(now) {
		until = now.Add(max(u.retryDelay, time.Minute))
	}
	if until.After(u.retryAt) {
		u.retryAt = until
	}
	if limited {
		return fmt.Errorf("GitHub rate limit reached (HTTP %d); retry after %s", response.StatusCode, u.retryAt.UTC().Format(time.RFC3339))
	}
	return nil
}

func trustedAssetSource(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || !httpsURL(parsed) || parsed.Host != "github.com" || parsed.RawQuery != "" || parsed.RawPath != "" {
		return false
	}
	const prefix = "/idy/tailsplit/releases/download/"
	if !strings.HasPrefix(parsed.Path, prefix) {
		return false
	}
	tag, name, ok := strings.Cut(strings.TrimPrefix(parsed.Path, prefix), "/")
	_, stable := stableVersion(tag)
	return ok && stable && name != "" && !strings.ContainsAny(name, "/\\") && raw == assetURLBase+tag+"/"+name
}

func httpsURL(parsed *url.URL) bool {
	return parsed != nil && parsed.Scheme == "https" && parsed.User == nil && parsed.Opaque == "" &&
		parsed.Fragment == "" && parsed.Host != ""
}

func trustedAssetRedirect(parsed *url.URL, source string) bool {
	if !httpsURL(parsed) {
		return false
	}
	switch parsed.Host {
	case "github.com":
		return parsed.String() == source
	case "release-assets.githubusercontent.com", "objects.githubusercontent.com", "github-releases.githubusercontent.com":
		return true
	default:
		return false
	}
}

func readBounded(response *http.Response, limit int64) ([]byte, error) {
	if response.ContentLength > limit {
		return nil, errors.New("response exceeds the size limit")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("response exceeds the size limit")
	}
	return data, nil
}
