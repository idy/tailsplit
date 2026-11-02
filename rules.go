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
	"time"

	"github.com/idy/tailsplit/internal/gfwlistdns"
)

// RefreshRules updates only the validated GFWList cache. Running flows, login
// state and fake-IP mappings are untouched; restart to load the new policy.
func RefreshRules(ctx context.Context, config Config, client *http.Client) (gfwlistdns.RulesInfo, error) {
	source := config.Rules.GFWList
	if source == "builtin" {
		source = gfwlistdns.DefaultRulesURL
	}
	parsed, err := url.Parse(source)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return gfwlistdns.RulesInfo{}, errors.New("rules refresh requires builtin or an HTTPS GFWList URL")
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
		return gfwlistdns.RulesInfo{}, err
	}
	response, err := copyClient.Do(request)
	if err != nil {
		// A URL may include signed query parameters. Do not echo it in errors.
		return gfwlistdns.RulesInfo{}, errors.New("GFWList download failed; existing cache retained")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return gfwlistdns.RulesInfo{}, fmt.Errorf("GFWList returned HTTP %d; existing cache retained", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil {
		return gfwlistdns.RulesInfo{}, err
	}
	projection, err := gfwlistdns.ProjectActiveRules(data)
	if err != nil {
		return gfwlistdns.RulesInfo{}, fmt.Errorf("invalid GFWList; existing cache retained: %w", err)
	}
	if err := privateState(config.StateDir); err != nil {
		return gfwlistdns.RulesInfo{}, err
	}
	file, err := os.CreateTemp(config.StateDir, ".gfwlist-*")
	if err != nil {
		return gfwlistdns.RulesInfo{}, err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return gfwlistdns.RulesInfo{}, err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return gfwlistdns.RulesInfo{}, err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return gfwlistdns.RulesInfo{}, err
	}
	if err := file.Close(); err != nil {
		return gfwlistdns.RulesInfo{}, err
	}
	if err := os.Rename(file.Name(), filepath.Join(config.StateDir, "gfwlist.txt")); err != nil {
		return gfwlistdns.RulesInfo{}, err
	}
	dir, err := os.Open(config.StateDir)
	if err != nil {
		return projection.Info, errors.New("rules installed but directory durability could not be confirmed")
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return projection.Info, errors.New("rules installed but directory durability could not be confirmed")
	}
	return projection.Info, nil
}
