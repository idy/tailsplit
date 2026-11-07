package tailsplit

import (
	"context"
	"crypto/sha256"
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

func rulesSubscriptionSource(source string) string {
	if source == "builtin" {
		return domainrules.DefaultRulesURL
	}
	return source
}

// RulesCachePath binds every remote subscription to its canonical source URL.
// Query credentials are hashed rather than exposed in the filename.
func RulesCachePath(config Config) string {
	name := fmt.Sprintf("gfwlist-%x.txt", sha256.Sum256([]byte(rulesSubscriptionSource(config.Rules.GFWList))))
	return filepath.Join(config.StateDir, name)
}

func migrateLegacyRules(config Config) (bool, error) {
	if config.Rules.Mode == RoutingGlobal || config.Rules.GFWList == "" ||
		config.Rules.GFWList != "builtin" && !strings.HasPrefix(config.Rules.GFWList, "https://") {
		return false, nil
	}
	if config.rulesCacheSource != rulesSubscriptionSource(config.Rules.GFWList) ||
		config.rulesCacheStateDir != config.StateDir {
		return false, nil
	}
	target := RulesCachePath(config)
	if _, err := os.Stat(target); err == nil {
		return true, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	legacy := filepath.Join(config.StateDir, "gfwlist.txt")
	info, err := os.Lstat(legacy)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, errors.New("legacy GFWList cache must be a regular file")
	}
	if _, err := readProjection(legacy); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("invalid legacy GFWList cache retained: %w", err)
	}
	installed, err := commitRuleCache(legacy, target)
	if err != nil {
		if installed {
			return true, errors.New("legacy rules cache migrated but directory durability could not be confirmed")
		}
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// EnsureRules downloads remote rules only when the matching cache is missing.
// Empty, local-file and global-mode policies do not require a download.
func EnsureRules(ctx context.Context, config Config, client *http.Client) error {
	if config.Rules.Mode == RoutingGlobal || config.Rules.GFWList == "" ||
		config.Rules.GFWList != "builtin" && !strings.HasPrefix(config.Rules.GFWList, "https://") {
		return nil
	}
	if _, err := os.Stat(RulesCachePath(config)); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if migrated, err := migrateLegacyRules(config); err != nil {
		return err
	} else if migrated {
		return nil
	}
	// Another reader may have completed the same migration meanwhile.
	if _, err := os.Stat(RulesCachePath(config)); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if _, err := RefreshRules(ctx, config, client); err != nil {
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
	installed, err := commitRuleCache(file.Name(), RulesCachePath(config))
	if err != nil {
		if installed {
			return projection.Info, errors.New("rules installed but directory durability could not be confirmed")
		}
		return domainrules.RulesInfo{}, err
	}
	return projection.Info, nil
}
