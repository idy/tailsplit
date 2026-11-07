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

	"golang.org/x/net/idna"
)

// DefaultRulesURL is the official, base64-encoded GFWList subscription.
const DefaultRulesURL = "https://raw.githubusercontent.com/gfwlist/gfwlist/master/gfwlist.txt"

const maxRulesSize = 2 << 20

//go:embed supplemental.txt
var supplementalRules []byte

// These domains moved upstream. Preserve prior coverage only when the selected
// list does not already cover the whole domain and its subdomains.
var upstreamApplicationDomains = [...]string{
	"openai.com", "chatgpt.com", "anthropic.com", "claude.ai", "claude.com",
	"claude.app", "claudeusercontent.com", "claudemcpcontent.com",
}

// RulesInfo keeps GFWList and effective supplemental diagnostics separate.
type RulesInfo struct {
	SHA256             string
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

func (r domainRules) coversSuffix(domain string) bool {
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
		if strings.HasPrefix(pattern.String(), `^(?:[^.]+\.)*`) && pattern.MatchString(domain) {
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
	extra := bytes.Clone(supplementalRules)
	for _, domain := range upstreamApplicationDomains {
		if !r.proxy.coversSuffix(domain) && !r.direct.coversSuffix(domain) {
			extra = fmt.Appendf(extra, "\n||%s", domain)
		}
	}
	supplement, _, err := parseRules(extra)
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
		return nil, nil, fmt.Errorf("domainrules: rule list exceeds %d bytes", maxRulesSize)
	}
	trim := func(text []byte) []byte {
		return bytes.TrimSpace(bytes.TrimPrefix(bytes.TrimSpace(text), []byte("\xef\xbb\xbf")))
	}
	data = trim(data)
	if len(data) == 0 {
		return nil, nil, fmt.Errorf("domainrules: empty rule list")
	}
	// Domain and AutoProxy text contains punctuation outside the standard Base64
	// alphabet. Decode only complete Base64 input; otherwise parse it as text.
	encoded := string(bytes.Join(bytes.Fields(data), nil))
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(encoded)
	}
	if err == nil {
		data = trim(decoded)
	}
	r := &rules{
		proxy:  domainRules{exact: map[string]bool{}, suffix: map[string]bool{}},
		direct: domainRules{exact: map[string]bool{}, suffix: map[string]bool{}},
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "!") || strings.HasPrefix(line, "#") {
			continue
		}
		// Reject markup lines even when an error page includes standalone domains.
		// Angle brackets inside regex or URL filters remain skippable syntax.
		if strings.HasPrefix(line, "<") {
			return nil, nil, fmt.Errorf("domainrules: HTML is not a rule list")
		}
		if strings.HasPrefix(line, "[AutoProxy ") {
			if !strings.HasSuffix(line, "]") {
				return nil, nil, fmt.Errorf("domainrules: invalid rule list header")
			}
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
		return nil, nil, fmt.Errorf("domainrules: no usable proxy domains in rule list")
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
