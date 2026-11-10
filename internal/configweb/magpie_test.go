package configweb

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestMagpieDependencyAssetsAreServedLocally(t *testing.T) {
	server, _, _ := testServer(t)
	paths := map[string]string{
		"/vendor/magpie/css/Magpie-MIT.txt":   "text/plain",
		"/ui.js":                              "javascript",
		"/select.js":                          "javascript",
		"/vendor/magpie/js/select.js":         "javascript",
		"/vendor/magpie/css/magpie.css":       "text/css",
		"/vendor/magpie/css/tokens.css":       "text/css",
		"/vendor/magpie/css/fonts.css":        "text/css",
		"/vendor/magpie/css/base.css":         "text/css",
		"/vendor/magpie/css/components.css":   "text/css",
		"/vendor/magpie/css/layout.css":       "text/css",
		"/vendor/magpie/icons/icons.svg":      "image/svg+xml",
		"/vendor/magpie/icons/caret-down.svg": "image/svg+xml",
		"/vendor/magpie/icons/check.svg":      "image/svg+xml",
		"/vendor/magpie/icons/spinner.svg":    "image/svg+xml",
	}
	for _, font := range []string{"montserrat-latin", "monda-latin", "lato-regular-latin", "lato-bold-latin"} {
		paths["/vendor/magpie/fonts/"+font+".woff2"] = "font/woff2"
	}
	for path, mime := range paths {
		t.Run(path, func(t *testing.T) {
			response, err := http.Get("http://" + server.address + path)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			data, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != http.StatusOK || len(data) == 0 {
				t.Fatalf("asset response: status %d, %d bytes", response.StatusCode, len(data))
			}
			if got := response.Header.Get("Content-Type"); !strings.Contains(got, mime) {
				t.Fatalf("asset MIME type: got %q, want %q", got, mime)
			}
			if mime == "font/woff2" && !bytes.HasPrefix(data, []byte("wOF2")) {
				t.Fatal("font is not WOFF2")
			}
			policy := response.Header.Get("Content-Security-Policy")
			if !strings.Contains(policy, "script-src 'self';") || !strings.Contains(policy, "font-src 'self';") || strings.Contains(policy, "unsafe-") {
				t.Fatalf("asset weakened policy: %q", policy)
			}
		})
	}
}

func TestMagpieAssetsRetainStaticBoundaries(t *testing.T) {
	server, _, _ := testServer(t)
	for _, path := range []string{
		"/vendor/magpie/", "/vendor/magpie/css/", "/vendor/magpie/js/",
		"/vendor/magpie/README.tailsplit.md", "/vendor/magpie/manifest.json",
		"/vendor/magpie/fonts/README.md", "/vendor/magpie/icons/manifest.json",
		"/vendor/magpie/js/missing.js", "/vendor/magpie/../peetty/README.md",
	} {
		status, _ := request(t, server, "GET", path, nil, nil)
		if status != http.StatusNotFound {
			t.Fatalf("unlisted asset %s: status %d", path, status)
		}
	}
	for _, path := range []string{"/ui.js", "/vendor/magpie/css/magpie.css", "/vendor/magpie/js/select.js", "/vendor/magpie/fonts/monda-latin.woff2"} {
		for _, method := range []string{"HEAD", "POST"} {
			status, _ := request(t, server, method, path, nil, nil)
			if status != http.StatusNotFound {
				t.Fatalf("asset %s accepted %s: %d", path, method, status)
			}
		}
		for _, headers := range []map[string]string{{"Host": "foreign.example"}, {"Origin": "https://foreign.example"}} {
			status, _ := request(t, server, "GET", path, nil, headers)
			if status != http.StatusForbidden {
				t.Fatalf("asset %s bypassed host/origin check: %d", path, status)
			}
		}
	}
}
