package configweb

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestBundledFontsAreServedWithLocalOnlyPolicy(t *testing.T) {
	server, _, _ := testServer(t)
	for _, name := range []string{
		"montserrat-latin.woff2", "monda-latin.woff2",
		"lato-regular-latin.woff2", "lato-bold-latin.woff2",
	} {
		t.Run(name, func(t *testing.T) {
			response, err := http.Get("http://" + server.address + "/fonts/" + name)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			data, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != http.StatusOK || !bytes.HasPrefix(data, []byte("wOF2")) {
				t.Fatalf("font response: status %d, %d bytes", response.StatusCode, len(data))
			}
			if got := response.Header.Get("Content-Type"); got != "font/woff2" {
				t.Fatalf("font MIME type: %q", got)
			}
			if !strings.Contains(response.Header.Get("Content-Security-Policy"), "font-src 'self';") {
				t.Fatal("font policy must permit only local assets")
			}
		})
	}
	for _, family := range []string{"Montserrat", "Monda", "Lato"} {
		status, data := request(t, server, "GET", "/fonts/"+family+"-OFL.txt", nil, nil)
		if status != http.StatusOK || !bytes.Contains(data, []byte("SIL OPEN FONT LICENSE Version 1.1")) {
			t.Fatalf("missing embedded license for %s", family)
		}
	}
}

func TestFontRoutesRetainStaticAssetBoundaries(t *testing.T) {
	server, _, _ := testServer(t)
	for _, path := range []string{"/fonts/", "/fonts/unknown.woff2", "/fonts/../style.css", "/fonts/../../config.yaml"} {
		status, _ := request(t, server, "GET", path, nil, nil)
		if status != http.StatusNotFound {
			t.Fatalf("unlisted asset %s: status %d", path, status)
		}
	}
	for _, headers := range []map[string]string{
		{"Host": "foreign.example"}, {"Origin": "https://foreign.example"},
	} {
		status, _ := request(t, server, "GET", "/fonts/monda-latin.woff2", nil, headers)
		if status != http.StatusForbidden {
			t.Fatalf("font bypassed host/origin check: %d", status)
		}
	}
	status, _ := request(t, server, "POST", "/fonts/monda-latin.woff2", nil, nil)
	if status != http.StatusNotFound {
		t.Fatalf("font accepted non-GET request: %d", status)
	}
}
