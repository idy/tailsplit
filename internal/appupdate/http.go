package appupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

func (u *Updater) fetchRelease(ctx context.Context) (githubRelease, error) {
	ctx, cancel := context.WithTimeout(ctx, u.requestTimeout)
	defer cancel()
	response, err := u.get(ctx, u.source, false)
	if err != nil {
		return githubRelease{}, err
	}
	defer response.Body.Close()
	data, err := readBounded(response, maxJSONBytes)
	if err != nil {
		return githubRelease{}, fmt.Errorf("read release metadata: %w", err)
	}
	var release githubRelease
	if err := json.Unmarshal(data, &release); err != nil {
		return githubRelease{}, fmt.Errorf("decode release metadata: %w", err)
	}
	return release, nil
}

func (u *Updater) get(ctx context.Context, source string, asset bool) (*http.Response, error) {
	if !asset && source != latestReleaseURL || asset && !trustedAssetSource(source) {
		return nil, errors.New("update source is not the expected public GitHub release URL")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, fmt.Errorf("create update request: %w", err)
	}
	request.Header.Set("User-Agent", "TailSplit-Updater")
	if asset {
		request.Header.Set("Accept", "application/octet-stream")
	} else {
		request.Header.Set("Accept", "application/vnd.github+json")
		request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}
	client := *u.client
	client.Jar = nil
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if !asset {
			return errors.New("release metadata redirects are not allowed")
		}
		if len(via) >= 5 {
			return errors.New("too many release asset redirects")
		}
		if !trustedAssetRedirect(next.URL, source) {
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
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		if response.StatusCode == http.StatusTooManyRequests ||
			response.StatusCode == http.StatusForbidden && response.Header.Get("X-RateLimit-Remaining") == "0" {
			return nil, fmt.Errorf("GitHub rate limit reached (HTTP %d)", response.StatusCode)
		}
		return nil, fmt.Errorf("GitHub release request failed (HTTP %d %s)", response.StatusCode, http.StatusText(response.StatusCode))
	}
	return response, nil
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
