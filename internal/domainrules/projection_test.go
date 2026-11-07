package domainrules

import (
	"encoding/base64"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestProjectionKeepsExceptionsAndURLBoundaries(t *testing.T) {
	result, err := ProjectRules([]byte(`[AutoProxy 0.2.9]
||example.com
|https://exact.example.net/
||*.wild.example.net
@@||direct.example.com
@@||example.org/private/path
||example.net/one/path
`))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Proxy.Exact, []string{"exact.example.net"}) ||
		!reflect.DeepEqual(result.Proxy.Suffix, []string{"example.com"}) ||
		!reflect.DeepEqual(result.Direct.Suffix, []string{"direct.example.com"}) {
		t.Fatalf("incorrect domain projection: %+v", result)
	}
	if len(result.Proxy.Regex) != 1 || result.Info.Skipped != 2 {
		t.Fatalf("lost wildcard or widened URL rule: %+v", result)
	}
	result.Proxy.Exact[0] = "changed.example"
	again, err := ProjectRules([]byte("[AutoProxy 0.2.9]\n||example.com\n"))
	if err != nil || !reflect.DeepEqual(again.Proxy.Suffix, []string{"example.com"}) {
		t.Fatalf("projections share mutable state: %+v, %v", again, err)
	}
}

func TestProjectionContainsOnlySuppliedRules(t *testing.T) {
	data := []byte("[AutoProxy 0.2.9]\n||fixture.example\n@@||safe.fixture.example\n")
	encoded := []byte(base64.StdEncoding.EncodeToString(data))
	for _, source := range [][]byte{data, encoded} {
		projection, err := ProjectRules(source)
		if err != nil {
			t.Fatal(err)
		}
		parsed, _, err := parseRules(source)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(projection.Proxy.Suffix, []string{"fixture.example"}) ||
			!reflect.DeepEqual(projection.Direct.Suffix, []string{"safe.fixture.example"}) ||
			len(projection.Proxy.Exact)+len(projection.Proxy.Regex) != 0 {
			t.Fatalf("projection added rules outside the supplied list: %+v", projection)
		}
		for _, test := range []struct {
			domain string
			proxy  bool
		}{
			{"fixture.example", true},
			{"a.fixture.example", true},
			{"safe.fixture.example", false},
			{"a.safe.fixture.example", false},
			{"notfixture.example", false},
			{"setup.workos.com", false},
			{"stripe.com", false},
			{"chatgpt.com", false},
		} {
			projected := !projectionMatches(projection.Direct, test.domain) && projectionMatches(projection.Proxy, test.domain)
			if projected != test.proxy || parsed.match(test.domain) != test.proxy {
				t.Fatalf("unexpected proxy policy for %s: projected=%t, want=%t", test.domain, projected, test.proxy)
			}
		}
		if projection.Info != parsed.info || projection.Info.Proxy != 1 || projection.Info.Direct != 1 {
			t.Fatal("projection diagnostics differ from the supplied rules")
		}
	}
}

func TestActiveProjectionIncludesSupplementAndHonorsExceptions(t *testing.T) {
	data := []byte("[AutoProxy 0.2.9]\n||fixture.example\n||chatgpt.com\n@@||safe.stripe.com\n")
	encoded := []byte(base64.StdEncoding.EncodeToString(data))
	for _, source := range [][]byte{data, encoded} {
		projection, err := ProjectActiveRules(source)
		if err != nil {
			t.Fatal(err)
		}
		parsed, _, err := parseActiveRules(source)
		if err != nil {
			t.Fatal(err)
		}
		for _, test := range []struct {
			domain string
			proxy  bool
		}{
			{"fixture.example", true},
			{"chatgpt.com", true},
			{"a.chatgpt.com", true},
			{"safe.stripe.com", false},
			{"a.safe.stripe.com", false},
			{"setup.workos.com", true},
			{"a.setup.workos.com", false},
			{"stripe.com", true},
			{"checkout.stripe.com", true},
			{"notstripe.com", false},
			{"example.invalid", false},
		} {
			projected := !projectionMatches(projection.Direct, test.domain) && projectionMatches(projection.Proxy, test.domain)
			if projected != test.proxy || parsed.match(test.domain) != test.proxy {
				t.Fatalf("unexpected active policy for %s: got=%t, want=%t", test.domain, projected, test.proxy)
			}
		}
		upstream, err := ProjectRules(source)
		if err != nil {
			t.Fatal(err)
		}
		if projection.Info != parsed.info || projection.Info.SHA256 != upstream.Info.SHA256 ||
			projection.Info.Proxy != upstream.Info.Proxy || projection.Info.Direct != upstream.Info.Direct ||
			projection.Info.SupplementalProxy == 0 || projection.Info.SupplementalSHA256 == "" {
			t.Fatal("supplement diagnostics were omitted or mixed into the supplied list")
		}
	}
}

func TestCompatibilityOnlyFillsMissingCoverage(t *testing.T) {
	legacyCoverage := "||openai.com\n||chatgpt.com\n||anthropic.com\n||claude.ai\n||claude.com\n"
	otherCoverage := legacyCoverage + "||claudeusercontent.com\n||claudemcpcontent.com\n"
	base, err := ProjectRules(supplementalRules)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name       string
		rules      string
		extra      int
		proxyRoot  bool
		proxyChild bool
	}{
		{"current upstream", otherCoverage + "||claude.app\n", 0, true, true},
		{"older snapshot", legacyCoverage, 3, true, true},
		{"exact proxy host", otherCoverage + "|claude.app|\n", 1, true, true},
		{"full direct exception", otherCoverage + "@@||claude.app\n", 0, false, false},
		{"exact direct exception", otherCoverage + "@@|claude.app|\n", 1, false, true},
		{"wildcard suffix coverage", otherCoverage + "||clau*.app\n", 0, true, true},
		{"subdomains only", otherCoverage + "||*.claude.app\n", 1, true, true},
		{"direct subdomains only", otherCoverage + "@@||*.claude.app\n", 1, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			projection, err := ProjectActiveRules([]byte("[AutoProxy 0.2.9]\n||fixture.example\n" + test.rules))
			if err != nil {
				t.Fatal(err)
			}
			if projection.Info.SupplementalProxy != base.Info.Proxy+test.extra {
				t.Fatalf("compatibility added redundant or insufficient rules: got=%d, want=%d", projection.Info.SupplementalProxy, base.Info.Proxy+test.extra)
			}
			if test.extra == 0 && projection.Info.SupplementalSHA256 != base.Info.SHA256 {
				t.Fatal("already covered domains changed the effective supplement")
			}
			for domain, want := range map[string]bool{"claude.app": test.proxyRoot, "a.claude.app": test.proxyChild} {
				proxy := !projectionMatches(projection.Direct, domain) && projectionMatches(projection.Proxy, domain)
				if proxy != want {
					t.Fatalf("wrong proxy coverage for %s: got=%t, want=%t", domain, proxy, want)
				}
			}
		})
	}
}

func TestRuleListFormatsHaveEquivalentProjections(t *testing.T) {
	body := "first.example\n||fixture.example\n|https://exact.example.net/\n||*.wild.example.net\n@@||safe.fixture.example\n@@direct.example.net\n@@||safe.stripe.com\n||path.example.org/private\n! comment\n"
	plain := "[AutoProxy 0.2.9]\n" + body
	encoded := base64.StdEncoding.EncodeToString([]byte(plain))
	encodedBody := base64.StdEncoding.EncodeToString([]byte(body))
	if !strings.HasSuffix(encoded, "=") || !strings.HasSuffix(encodedBody, "=") {
		t.Fatal("fixtures must exercise omitted Base64 padding")
	}
	formats := []struct{ name, data string }{
		{"header", plain},
		{"headerless", body},
		{"headerless CRLF", strings.ReplaceAll(body, "\n", "\r\n")},
		{"BOM header", "\xef\xbb\xbf" + plain},
		{"BOM headerless", "\xef\xbb\xbf" + body},
		{"comments before header", "# custom rule list\n! metadata\n" + plain},
		{"Base64", encoded},
		{"wrapped Base64", encoded[:64] + "\r\n" + encoded[64:] + "\r\n"},
		{"unpadded Base64", strings.TrimRight(encoded, "=")},
		{"Base64 headerless", encodedBody},
		{"wrapped unpadded headerless", "\xef\xbb\xbf \n" + encodedBody[:64] + "\r\n" + strings.TrimRight(encodedBody[64:], "=")},
		{"Base64 decoded BOM", base64.StdEncoding.EncodeToString([]byte("\xef\xbb\xbf" + plain))},
		{"Base64 decoded BOM headerless", base64.StdEncoding.EncodeToString([]byte("\xef\xbb\xbf# custom rule list\n" + body))},
	}
	for _, projector := range []struct {
		name    string
		project func([]byte) (Projection, error)
	}{
		{"supplied", ProjectRules},
		{"active", ProjectActiveRules},
	} {
		want, err := projector.project([]byte(plain))
		if err != nil {
			t.Fatal(err)
		}
		if want.Info.Proxy != 4 || want.Info.Direct != 3 || want.Info.Skipped != 1 {
			t.Fatalf("unexpected fixture policy: %+v", want)
		}
		want.Info.SHA256 = "" // The digest identifies the decoded source text, including its optional header.
		for _, format := range formats {
			t.Run(projector.name+"/"+format.name, func(t *testing.T) {
				got, err := projector.project([]byte(format.data))
				if err != nil {
					t.Fatal(err)
				}
				if got.Info.SHA256 == "" {
					t.Fatal("missing source digest")
				}
				got.Info.SHA256 = ""
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("format changed rule projection: got=%+v, want=%+v", got, want)
				}
			})
		}
	}
}

func TestUnsupportedFiltersWithAngleBracketsRemainSkippable(t *testing.T) {
	for _, filter := range []string{
		`/(?<host>example\.com)/`, `/(?<!foo)example\.com/`, `/foo[<>]bar/`,
		`@@/(?<host>example\.com)/`, `||example.com/path?q=<value>`,
		`https://example.com/<path>`,
	} {
		for _, header := range []string{"", "[AutoProxy 0.2.9]\n"} {
			data := []byte(header + "||example.com\n" + filter + "\n")
			for _, source := range [][]byte{data, []byte(base64.StdEncoding.EncodeToString(data))} {
				projection, err := ProjectRules(source)
				if err != nil || projection.Info.Proxy != 1 || projection.Info.Skipped != 1 {
					t.Fatalf("unsupported filter invalidated usable domains: %q, %+v, %v", filter, projection, err)
				}
			}
		}
	}
}

func TestHeaderlessRuleListKeepsFirstException(t *testing.T) {
	projection, err := ProjectRules([]byte("@@||safe.example.com\n||example.com\n"))
	if err != nil {
		t.Fatal(err)
	}
	if projection.Info.Proxy != 1 || projection.Info.Direct != 1 ||
		!reflect.DeepEqual(projection.Direct.Suffix, []string{"safe.example.com"}) {
		t.Fatalf("first headerless rule was lost: %+v", projection)
	}
}

func TestRuleListsRejectInvalidResponses(t *testing.T) {
	for _, test := range []struct{ name, data string }{
		{"empty", ""},
		{"whitespace", " \t\r\n"},
		{"BOM only", "\xef\xbb\xbf"},
		{"comments only", "! example.com\n# example.net\n"},
		{"exceptions only", "@@||direct.example\n"},
		{"nonsense", "upstream unavailable\nretry later\n"},
		{"malformed Base64", "W0F1dG9Qcm94eSAwLjIuOV0=%%"},
		{"malformed header", "[AutoProxy 0.2.9\n||example.com\n"},
		{"HTML error with domain", "<!DOCTYPE html>\n<html><body>\nexample.com\n</body></html>"},
		{"HTML after domain", "example.com\n<BR>\n"},
		{"HTML with header", "[AutoProxy 0.2.9]\n||example.com\n</html>"},
		{"hosts", "0.0.0.0 example.com\n127.0.0.1 safe.example.com\n"},
		{"Clash", "payload:\n  - DOMAIN-SUFFIX,example.com\n"},
		{"JSON", "{\"rules\": [\"example.com\"]}"},
		{"unsupported only", "[AutoProxy 0.2.9]\n||example.com/private\n@@||safe.example.com\n"},
	} {
		for _, format := range []struct{ name, data string }{
			{"text", test.data},
			{"Base64", base64.StdEncoding.EncodeToString([]byte(test.data))},
			{"unpadded Base64", base64.RawStdEncoding.EncodeToString([]byte(test.data))},
		} {
			t.Run(test.name+"/"+format.name, func(t *testing.T) {
				for _, project := range []func([]byte) (Projection, error){ProjectRules, ProjectActiveRules} {
					if _, err := project([]byte(format.data)); err == nil {
						t.Fatal("accepted an invalid rule list")
					} else if !strings.Contains(err.Error(), "rule list") {
						t.Fatalf("error does not describe a generic rule list: %v", err)
					}
				}
			})
		}
	}
}

func projectionMatches(projected DomainProjection, domain string) bool {
	if slices.Contains(projected.Exact, domain) {
		return true
	}
	for _, suffix := range projected.Suffix {
		if domain == suffix || strings.HasSuffix(domain, "."+suffix) {
			return true
		}
	}
	for _, expression := range projected.Regex {
		if regexp.MustCompile(expression).MatchString(domain) {
			return true
		}
	}
	return false
}
