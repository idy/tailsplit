package gfwlistdns

import (
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

func TestActiveProjectionMatchesTheInstalledDNSPolicy(t *testing.T) {
	data := []byte("[AutoProxy 0.2.9]\n||fixture.example\n@@||safe.chatgpt.com\n")
	for _, source := range [][]byte{builtinRules, data} {
		projection, err := ProjectActiveRules(source)
		if err != nil {
			t.Fatal(err)
		}
		active, _, err := parseActiveRules(source)
		if err != nil {
			t.Fatal(err)
		}
		for _, domain := range []string{"setup.workos.com", "a.setup.workos.com", "claudemcpcontent.com",
			"api.statsig.com", "a.chatgpt.com", "safe.chatgpt.com", "fixture.example", "example.invalid"} {
			projected := !projectionMatches(projection.Direct, domain) && projectionMatches(projection.Proxy, domain)
			if projected != active.match(domain) {
				t.Fatalf("projected policy differs from the installed DNS policy for %s", domain)
			}
		}
		if projection.Info != active.info || projection.Info.SupplementalProxy == 0 {
			t.Fatal("supplement provenance was omitted or mixed into upstream diagnostics")
		}
	}
	builtin, err := BuiltinProjection()
	if err != nil || !projectionMatches(builtin.Proxy, "setup.workos.com") {
		t.Fatal("the builtin routing policy omitted its bundled application supplement")
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
