package domainrules

import (
	"slices"
	"sort"
)

// DomainProjection is a domain-level view of AutoProxy rules. Exact, Suffix and
// Regex use whole-domain matching; they never represent URL paths or schemes.
type DomainProjection struct {
	Exact  []string
	Suffix []string
	Regex  []string
}

// Projection contains domain rules and diagnostics from an AutoProxy list.
// Direct exceptions must be matched before Proxy. Info reports skipped URL
// rules; this is a domain projection, not a full AutoProxy interpreter.
type Projection struct {
	Proxy  DomainProjection
	Direct DomainProjection
	Info   RulesInfo
}

// ProjectActiveRules includes the supplement while preserving direct exceptions
// and keeping its diagnostics separate from the supplied list.
func ProjectActiveRules(data []byte) (Projection, error) {
	parsed, _, err := parseActiveRules(data)
	if err != nil {
		return Projection{}, err
	}
	proxy := projectDomains(parsed.proxy)
	supplement := projectDomains(parsed.supplemental)
	merge := func(left, right []string) []string {
		values := append(left, right...)
		sort.Strings(values)
		return slices.Compact(values)
	}
	proxy.Exact = merge(proxy.Exact, supplement.Exact)
	proxy.Suffix = merge(proxy.Suffix, supplement.Suffix)
	proxy.Regex = merge(proxy.Regex, supplement.Regex)
	return Projection{Proxy: proxy, Direct: projectDomains(parsed.direct), Info: parsed.info}, nil
}

// ProjectRules conservatively projects the supplied AutoProxy list into domain
// rules, preserving direct exceptions.
func ProjectRules(data []byte) (Projection, error) {
	parsed, _, err := parseRules(data)
	if err != nil {
		return Projection{}, err
	}
	return Projection{
		Proxy:  projectDomains(parsed.proxy),
		Direct: projectDomains(parsed.direct),
		Info:   parsed.info,
	}, nil
}

func projectDomains(rules domainRules) DomainProjection {
	var result DomainProjection
	for value := range rules.exact {
		result.Exact = append(result.Exact, value)
	}
	for value := range rules.suffix {
		result.Suffix = append(result.Suffix, value)
	}
	for _, expression := range rules.wildcard {
		result.Regex = append(result.Regex, expression.String())
	}
	sort.Strings(result.Exact)
	sort.Strings(result.Suffix)
	sort.Strings(result.Regex)
	return result
}
