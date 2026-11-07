// Package pingenv prevents an idle Ping from starting an implicit login.
package pingenv

import "strings"

// These are the environment fallbacks used by the embedded Tailscale client
// for auth keys, OAuth, workload identity and forced login.
func authenticationSetting(entry string) bool {
	name, value, ok := strings.Cut(entry, "=")
	if !ok || value == "" {
		return false
	}
	for _, key := range [...]string{
		"TS_AUTHKEY", "TS_AUTH_KEY", "TS_CLIENT_SECRET", "TS_CLIENT_ID",
		"TS_ID_TOKEN", "TS_AUDIENCE", "TSNET_FORCE_LOGIN",
	} {
		if strings.EqualFold(name, key) {
			return true
		}
	}
	return false
}

// HasAuthentication reports whether a process can implicitly authenticate.
func HasAuthentication(env []string) bool {
	for _, entry := range env {
		if authenticationSetting(entry) {
			return true
		}
	}
	return false
}

// WithoutAuthentication returns an environment safe for a Ping helper.
func WithoutAuthentication(env []string) []string {
	clean := make([]string, 0, len(env))
	for _, entry := range env {
		if !authenticationSetting(entry) {
			clean = append(clean, entry)
		}
	}
	return clean
}
