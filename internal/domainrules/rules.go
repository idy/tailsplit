// Package domainrules parses AutoProxy lists and projects domain routing rules.
package domainrules

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"fmt"
	"net/netip"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/idna"
)

// DefaultRulesURL is the official, base64-encoded GFWList subscription.
const DefaultRulesURL = "https://raw.githubusercontent.com/gfwlist/gfwlist/master/gfwlist.txt"

const maxRulesSize = 2 << 20

//go:embed supplemental.txt
var supplementalRules []byte

// RulesInfo keeps GFWList and supplemental diagnostics separate.
type RulesInfo struct {
	SHA256             string
	LastModified       time.Time
	Proxy              int
	Direct             int
	Skipped            int
	CacheError         string // Startup fallback reason or an installed update's durability warning.
	SupplementalSHA256 string
	SupplementalProxy  int
}

type domainRules struct {
	exact    map[string]bool
	suffix   map[string]bool
	wildcard []*regexp.Regexp
}

type rules struct {
	proxy, direct domainRules
	supplemental  domainRules
	info          RulesInfo
}

func (r domainRules) match(domain string) bool {
	if r.exact[domain] {
		return true
	}
	for rest := domain; ; {
		if r.suffix[rest] {
			return true
		}
		i := strings.IndexByte(rest, '.')
		if i < 0 {
			break
		}
		rest = rest[i+1:]
	}
	for _, pattern := range r.wildcard {
		if pattern.MatchString(domain) {
			return true
		}
	}
	return false
}

func (r *rules) match(domain string) bool {
	return !r.direct.match(domain) && (r.proxy.match(domain) || r.supplemental.match(domain))
}

func parseActiveRules(data []byte) (*rules, []byte, error) {
	r, text, err := parseRules(data)
	if err != nil {
		return nil, nil, err
	}
	supplement, _, err := parseRules(supplementalRules)
	if err != nil {
		return nil, nil, fmt.Errorf("domainrules: bundled supplement: %w", err)
	}
	if supplement.info.Direct != 0 || supplement.info.Skipped != 0 {
		return nil, nil, fmt.Errorf("domainrules: bundled supplement must contain only supported proxy rules")
	}
	r.supplemental = supplement.proxy
	r.info.SupplementalSHA256 = supplement.info.SHA256
	r.info.SupplementalProxy = supplement.info.Proxy
	return r, text, nil
}

func parseRules(data []byte) (*rules, []byte, error) {
	if len(data) > maxRulesSize {
		return nil, nil, fmt.Errorf("domainrules: rules exceed %d bytes", maxRulesSize)
	}
	data = bytes.TrimSpace(data)
	if !bytes.HasPrefix(data, []byte("[AutoProxy ")) {
		decoded, err := base64.StdEncoding.DecodeString(string(bytes.Join(bytes.Fields(data), nil)))
		if err != nil {
			return nil, nil, fmt.Errorf("domainrules: decode GFWList: %w", err)
		}
		data = bytes.TrimSpace(decoded)
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 || !strings.HasPrefix(strings.TrimSpace(lines[0]), "[AutoProxy ") || !strings.HasSuffix(strings.TrimSpace(lines[0]), "]") {
		return nil, nil, fmt.Errorf("domainrules: missing AutoProxy header")
	}
	r := &rules{
		proxy:  domainRules{exact: map[string]bool{}, suffix: map[string]bool{}},
		direct: domainRules{exact: map[string]bool{}, suffix: map[string]bool{}},
	}
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if value, ok := strings.CutPrefix(line, "! Last Modified:"); ok {
			r.info.LastModified, _ = time.Parse(time.RFC1123Z, strings.TrimSpace(value))
		}
		if line == "" || strings.HasPrefix(line, "!") {
			continue
		}
		exception := strings.HasPrefix(line, "@@")
		line = strings.TrimPrefix(line, "@@")
		domain, suffix, ok := projectRule(line)
		if !ok {
			r.info.Skipped++
			continue
		}
		dst := &r.proxy
		if exception {
			dst = &r.direct
			r.info.Direct++
		} else {
			r.info.Proxy++
		}
		if strings.Contains(domain, "*") {
			pattern := regexp.QuoteMeta(domain)
			pattern = strings.ReplaceAll(pattern, `\*`, `[^.]*`)
			if suffix {
				pattern = `(?:[^.]+\.)*` + pattern
			}
			dst.wildcard = append(dst.wildcard, regexp.MustCompile("^"+pattern+"$"))
		} else if suffix {
			dst.suffix[domain] = true
		} else {
			dst.exact[domain] = true
		}
	}
	if r.info.Proxy == 0 {
		return nil, nil, fmt.Errorf("domainrules: no usable proxy domains in GFWList")
	}
	r.info.SHA256 = fmt.Sprintf("%x", sha256.Sum256(data))
	return r, bytes.Clone(data), nil
}

// projectRule deliberately does not promote path-specific rules to whole-host
// rules, for either proxy entries or exceptions. DNS cannot distinguish paths.
func projectRule(line string) (domain string, suffix, ok bool) {
	suffix = true
	switch {
	case strings.HasPrefix(line, "||"):
		line = line[2:]
	case strings.HasPrefix(line, "|"):
		line = line[1:]
		suffix = false
	case strings.HasPrefix(line, "."):
		line = line[1:]
	}
	if strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://") {
		line = strings.SplitN(line, "://", 2)[1]
		suffix = false
	}
	line = strings.TrimSuffix(line, "|")
	line = strings.TrimSuffix(line, "^")
	line = strings.TrimSuffix(line, "/")
	if strings.ContainsAny(line, "/:?$#|^\\") || strings.ContainsAny(line, " \t\r\n") {
		return "", false, false
	}
	// Validate wildcard labels with an ordinary placeholder before compiling.
	domain, err := canonicalDomain(strings.ReplaceAll(line, "*", "wildcard"))
	if err != nil || !strings.Contains(domain, ".") {
		return "", false, false
	}
	if _, err := netip.ParseAddr(domain); err == nil {
		return "", false, false
	}
	if strings.Contains(line, "*") {
		// GFWList wildcard hosts are ASCII. Avoid guessing Unicode glob syntax.
		for _, c := range line {
			if c > 127 {
				return "", false, false
			}
		}
		domain = strings.ToLower(strings.TrimSuffix(line, "."))
	}
	return domain, suffix, true
}

func canonicalDomain(domain string) (string, error) {
	domain, err := idna.Lookup.ToASCII(strings.TrimSuffix(domain, "."))
	if err != nil {
		return "", err
	}
	domain = strings.ToLower(domain)
	if len(domain) == 0 || len(domain) > 253 {
		return "", fmt.Errorf("invalid domain length")
	}
	for _, label := range strings.Split(domain, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("invalid domain label")
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return "", fmt.Errorf("invalid domain character")
			}
		}
	}
	return domain, nil
}
