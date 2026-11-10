package tray

import (
	"errors"
	"strings"
)

// The configuration window loads only its own panel origin. Like the Windows
// host, check the authority ourselves rather than accept a URL parser's
// normalization of credentials, backslashes, alternate loopback spellings or
// other schemes.
const panelOrigin = "http://127.0.0.1"

var errPanelURL = errors.New("configuration window URL must use http://127.0.0.1:<port> without credentials or a nonempty query")

// ConfigurationWindowPort validates the capability-bearing local UI URL without
// echoing it in errors. Desktop hosts share this policy with existing tests.
func ConfigurationWindowPort(raw string) (uint16, error) { return configurationWindowPort(raw) }

// ConfigurationWindowAllows confines WebView navigation to its original origin.
func ConfigurationWindowAllows(port uint16, raw string) bool {
	return configurationWindowAllows(port, raw)
}

// configurationWindowPort validates the URL the app opens in its window and
// returns the only port the window may load. The error never repeats the URL:
// its fragment carries the page capability.
func configurationWindowPort(raw string) (uint16, error) {
	port, ok := loopbackPagePort(raw, true)
	if !ok {
		return 0, errPanelURL
	}
	return port, nil
}

// configurationWindowAllows reports whether the window may navigate to, or
// accept a response from, candidate.
func configurationWindowAllows(port uint16, candidate string) bool {
	got, ok := loopbackPagePort(candidate, false)
	return ok && got == port
}

// loopbackPagePort returns the port of an http://127.0.0.1 URL. WebKit omits
// the default :80, so a later navigation may have no explicit port; the URL
// the app opens must name one and must not carry a query.
func loopbackPagePort(raw string, initial bool) (uint16, bool) {
	rest, ok := strings.CutPrefix(raw, panelOrigin)
	if !ok {
		return 0, false
	}
	port := 80
	if digits, ok := strings.CutPrefix(rest, ":"); ok {
		port = 0
		end := 0
		for end < len(digits) && digits[end] >= '0' && digits[end] <= '9' {
			port = port*10 + int(digits[end]-'0')
			if port > 65535 {
				return 0, false
			}
			end++
		}
		if end == 0 || port == 0 {
			return 0, false
		}
		rest = digits[end:]
	} else if initial {
		return 0, false
	}
	if rest != "" && rest[0] != '/' && rest[0] != '?' && rest[0] != '#' {
		return 0, false
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] < ' ' || raw[i] == 0x7f || raw[i] == '\\' {
			return 0, false
		}
	}
	if initial {
		query, fragment := strings.IndexByte(rest, '?'), strings.IndexByte(rest, '#')
		if query >= 0 && (fragment < 0 || query < fragment) && query+1 < len(rest) && rest[query+1] != '#' {
			return 0, false
		}
	}
	return uint16(port), true
}
