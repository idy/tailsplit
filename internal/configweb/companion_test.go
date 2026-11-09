package configweb

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCompanionAssetsAreLocalAndKeepRestrictiveCSP(t *testing.T) {
	server, _, _ := testServer(t)
	for _, path := range []string{
		"/companion.js", "/vendor/peetty/peetty.global.js",
		"/vendor/peetty/peetty.css", "/vendor/peetty/LICENSE.txt",
	} {
		t.Run(path, func(t *testing.T) {
			response, err := http.Get("http://" + server.address + path)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil || response.StatusCode != http.StatusOK || len(body) == 0 {
				t.Fatalf("embedded asset: status %d, %d bytes, error %v", response.StatusCode, len(body), err)
			}
			policy := response.Header.Get("Content-Security-Policy")
			for _, directive := range []string{"script-src 'self';", "style-src 'self';", "connect-src 'self';"} {
				if !strings.Contains(policy, directive) {
					t.Fatalf("companion must retain %s in %q", directive, policy)
				}
			}
			if strings.Contains(policy, "unsafe-inline") || strings.Contains(policy, "unsafe-eval") {
				t.Fatalf("companion weakened CSP: %q", policy)
			}
		})
	}
	for _, path := range []string{"/vendor/peetty/README.md", "/vendor/peetty/", "/vendor/peetty/chat/panel.js"} {
		status, _ := request(t, server, "GET", path, nil, nil)
		if status != http.StatusNotFound {
			t.Fatalf("non-public asset %s: status %d", path, status)
		}
	}
}
