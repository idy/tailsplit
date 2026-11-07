package tailsplit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/idy/tailsplit/internal/domainrules"
)

// This upstream revision covers the domains removed from the supplement.
// Older official caches must refresh before they can be used with that cleanup.
var minimumOfficialRulesDate = time.Date(2026, time.November, 6, 2, 29, 19, 0, time.UTC)

func officialRulesNeedUpgrade(source string, info domainrules.RulesInfo) bool {
	return (source == "builtin" || source == domainrules.DefaultRulesURL) &&
		!info.LastModified.IsZero() && info.LastModified.Before(minimumOfficialRulesDate)
}

// EnsureRules downloads missing remote rules and upgrades older official caches.
// Empty, local-file and global-mode policies do not require a download.
func EnsureRules(ctx context.Context, config Config, client *http.Client) error {
	if config.Rules.Mode == RoutingGlobal || config.Rules.GFWList == "" ||
		config.Rules.GFWList != "builtin" && !strings.HasPrefix(config.Rules.GFWList, "https://") {
		return nil
	}
	legacy := false
	cache := filepath.Join(config.StateDir, "gfwlist.txt")
	if _, err := os.Stat(cache); err == nil {
		if config.Rules.GFWList != "builtin" && config.Rules.GFWList != domainrules.DefaultRulesURL {
			return nil
		}
		projection, err := readProjection(cache)
		if err != nil {
			return err
		}
		if !officialRulesNeedUpgrade(config.Rules.GFWList, projection.Info) {
			return nil
		}
		legacy = true
	} else if !os.IsNotExist(err) {
		return err
	}
	if _, err := RefreshRules(ctx, config, client); err != nil {
		if legacy {
			return fmt.Errorf("cached official GFWList needs an update before use: %w", err)
		}
		return fmt.Errorf("no cached GFWList: %w; connect to the source or run tailsplit rules refresh first", err)
	}
	return nil
}

// RefreshRules updates only the validated GFWList cache. Running flows, login
// state and fake-IP mappings are untouched; restart to load the new policy.
func RefreshRules(ctx context.Context, config Config, client *http.Client) (domainrules.RulesInfo, error) {
	source := config.Rules.GFWList
	if source == "builtin" {
		source = domainrules.DefaultRulesURL
	}
	parsed, err := url.Parse(source)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return domainrules.RulesInfo{}, errors.New("rules refresh requires builtin or an HTTPS GFWList URL")
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	copyClient := *client
	copyClient.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if request.URL.Scheme != "https" || request.URL.User != nil || len(via) >= 10 {
			return errors.New("unsafe GFWList redirect")
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return domainrules.RulesInfo{}, err
	}
	response, err := copyClient.Do(request)
	if err != nil {
		// A URL may include signed query parameters. Do not echo it in errors.
		if ctx.Err() != nil {
			return domainrules.RulesInfo{}, fmt.Errorf("GFWList download failed; existing cache retained: %w", ctx.Err())
		}
		return domainrules.RulesInfo{}, errors.New("GFWList download failed; existing cache retained")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return domainrules.RulesInfo{}, fmt.Errorf("GFWList returned HTTP %d; existing cache retained", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil {
		return domainrules.RulesInfo{}, err
	}
	projection, err := domainrules.ProjectActiveRules(data)
	if err != nil {
		return domainrules.RulesInfo{}, fmt.Errorf("invalid GFWList; existing cache retained: %w", err)
	}
	if officialRulesNeedUpgrade(config.Rules.GFWList, projection.Info) {
		return domainrules.RulesInfo{}, errors.New("official GFWList is older than the supported rules; existing cache retained")
	}
	if err := privateState(config.StateDir); err != nil {
		return domainrules.RulesInfo{}, err
	}
	file, err := os.CreateTemp(config.StateDir, ".gfwlist-*")
	if err != nil {
		return domainrules.RulesInfo{}, err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return domainrules.RulesInfo{}, err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return domainrules.RulesInfo{}, err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return domainrules.RulesInfo{}, err
	}
	if err := file.Close(); err != nil {
		return domainrules.RulesInfo{}, err
	}
	installed, err := commitRuleCache(file.Name(), filepath.Join(config.StateDir, "gfwlist.txt"))
	if err != nil {
		if installed {
			return projection.Info, errors.New("rules installed but directory durability could not be confirmed")
		}
		return domainrules.RulesInfo{}, err
	}
	return projection.Info, nil
}
