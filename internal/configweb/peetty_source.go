package configweb

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	peettyGitHubAPI   = "https://api.github.com/repos/idy/peetty"
	peettySourceBase  = "https://raw.githubusercontent.com/idy/peetty/"
	peettyCacheFormat = 2
)

func peettyCommitID(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, digit := range value {
		if !(digit >= '0' && digit <= '9' || digit >= 'a' && digit <= 'f') {
			return false
		}
	}
	return true
}

// GitHub, not the delivery CDN, is the source authority. Tags in a different
// namespace and URLs supplied by remote metadata are never followed. Both
// lightweight and bounded annotated tags resolve to an immutable commit.
func (u *peettyUpdates) sourceCommit(ctx context.Context, version string) (string, error) {
	if !compatiblePeettyVersion(version) {
		return "", errors.New("incompatible Peetty source version")
	}
	source := peettyGitHubAPI + "/git/ref/tags/" + version
	for depth := 0; depth < 5; depth++ {
		data, err := u.fetch(ctx, source, 64<<10)
		if err != nil {
			return "", fmt.Errorf("resolve Peetty source tag: %w", err)
		}
		var result struct {
			Object struct {
				Type string
				SHA  string
			}
		}
		if err := json.Unmarshal(data, &result); err != nil || !peettyCommitID(result.Object.SHA) {
			return "", errors.New("invalid Peetty source tag")
		}
		switch result.Object.Type {
		case "commit":
			return result.Object.SHA, nil
		case "tag":
			source = peettyGitHubAPI + "/git/tags/" + result.Object.SHA
		default:
			return "", errors.New("Peetty source tag does not reference a commit")
		}
	}
	return "", errors.New("Peetty annotated tag chain exceeds limit")
}

func (u *peettyUpdates) downloadRelease(ctx context.Context, version string) (*peettyRelease, error) {
	commit, err := u.sourceCommit(ctx, version)
	if err != nil {
		return nil, err
	}
	release := &peettyRelease{
		Format: peettyCacheFormat, Commit: commit, Version: version,
		Files: make(map[string][]byte), Hashes: make(map[string]string),
	}
	for _, name := range []string{"cdn/peetty.global.js", "cdn/LICENSE", "cdn/THIRD_PARTY_NOTICES.txt"} {
		// Derive digests from the publisher repository independently of jsDelivr.
		// Fetch at the resolved commit, never a moving tag or CDN-provided URL.
		original, err := u.fetch(ctx, peettySourceBase+commit+"/"+name, peettyFileLimit)
		if err != nil {
			var status peettyHTTPStatus
			if name == "cdn/THIRD_PARTY_NOTICES.txt" && errors.As(err, &status) && status == 404 {
				continue
			}
			return nil, fmt.Errorf("read Peetty publisher asset %s: %w", name, err)
		}
		body, err := u.fetch(ctx, peettyCDNBase+commit+"/"+name, int64(len(original)))
		if err != nil {
			return nil, fmt.Errorf("download %s: %w", name, err)
		}
		expected, actual := sha256.Sum256(original), sha256.Sum256(body)
		if len(body) != len(original) || actual != expected {
			return nil, fmt.Errorf("Peetty CDN asset differs from publisher commit: %s", name)
		}
		release.Files[name], release.Hashes[name] = body, base64.StdEncoding.EncodeToString(expected[:])
	}
	return release, nil
}

type peettyHTTPStatus int

func (status peettyHTTPStatus) Error() string {
	return fmt.Sprintf("Peetty request returned HTTP %d", status)
}

func peettyAllowedSource(source string) bool {
	return source == peettyVersionsURL || strings.HasPrefix(source, peettyCDNBase) ||
		strings.HasPrefix(source, peettyGitHubAPI+"/git/") || strings.HasPrefix(source, peettySourceBase)
}
