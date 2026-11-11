package tray

import (
	"net/url"
	"testing"
)

func TestBrowserFallbackCanTagLoopbackURLsUnsupportedByNativeHost(t *testing.T) {
	const original = "http://[::1]:19790/#token=fixture-capability"
	if _, err := ConfigurationWindowPort(original); err == nil {
		t.Fatal("fixture unexpectedly satisfies the native host policy")
	}
	parsed, err := url.Parse(ConfigurationWindowConnectURL(original, "fallback-request"))
	if err != nil {
		t.Fatal(err)
	}
	fragment, err := url.ParseQuery(parsed.Fragment)
	if err != nil || parsed.Host != "[::1]:19790" || parsed.RawQuery != "" || fragment.Get("token") != "fixture-capability" || fragment.Get("connect") != "fallback-request" {
		t.Fatal("browser fallback lost its request or changed the server origin", parsed, err)
	}
}

func TestBrowserFallbackCarriesOnlyItsConnectIntentInFragment(t *testing.T) {
	const original = "http://127.0.0.1:19790/#token=fixture-capability"
	if got := ConfigurationWindowConnectURL(original, ""); got != original {
		t.Fatalf("ordinary open changed the panel URL: %q", got)
	}
	for _, request := range []string{"native-request", "request&token=other"} {
		got := ConfigurationWindowConnectURL(original, request)
		parsed, err := url.Parse(got)
		if err != nil {
			t.Fatal(err)
		}
		fragment, err := url.ParseQuery(parsed.Fragment)
		if err != nil {
			t.Fatal(err)
		}
		if parsed.Scheme != "http" || parsed.Host != "127.0.0.1:19790" || parsed.Path != "/" || parsed.RawQuery != "" || fragment.Get("token") != "fixture-capability" || fragment.Get("connect") != request {
			t.Fatal("fallback changed origin/capability or leaked intent into the request", got)
		}
		if port, err := ConfigurationWindowPort(got); err != nil || port != 19790 {
			t.Fatalf("fallback no longer satisfies the panel URL policy: %d, %v", port, err)
		}
	}
}
