package tray

import (
	"strings"
	"testing"
)

func TestConfigurationWindowAcceptsOnlyTheLocalPanelURL(t *testing.T) {
	// configweb.Server.URL keeps its capability in the fragment.
	token := strings.Repeat("ab", 32)
	for _, raw := range []string{
		"http://127.0.0.1:49152/#token=" + token,
		"http://127.0.0.1:19790/",
		"http://127.0.0.1:19790",
		"http://127.0.0.1:19790/?#token=" + token,
		"http://127.0.0.1:19790/#token=" + token + "&next=?a",
	} {
		port, err := configurationWindowPort(raw)
		if err != nil || (port != 49152 && port != 19790) {
			t.Fatalf("rejected panel URL %q: %d %v", raw, port, err)
		}
	}
	for _, raw := range []string{
		"",
		"http://127.0.0.1/#token=" + token,
		"http://127.0.0.1:/",
		"http://127.0.0.1:0/",
		"http://127.0.0.1:65536/",
		"http://127.0.0.1:1e3/",
		"http://127.0.0.1:19790/?token=" + token,
		"http://127.0.0.1:19790?x",
		"http://127.0.0.1:19790@example.com/",
		"http://user:pass@127.0.0.1:19790/",
		"http://127.0.0.1.example.com:19790/",
		"http://127.0.0.10:19790/",
		"http://localhost:19790/",
		"http://127.1:19790/",
		"http://[::1]:19790/",
		"https://127.0.0.1:19790/",
		"HTTP://127.0.0.1:19790/",
		"http://127.0.0.1:19790\\@example.com/",
		"http://127.0.0.1:19790/\n",
		"http://127.0.0.1:19790/\x7f",
		" http://127.0.0.1:19790/",
	} {
		if port, err := configurationWindowPort(raw); err == nil {
			t.Fatalf("accepted panel URL %q as port %d", raw, port)
		} else if strings.Contains(err.Error(), token) {
			t.Fatal("URL error repeats the page capability")
		}
	}
}

func TestConfigurationWindowNavigationStaysOnItsOrigin(t *testing.T) {
	for _, candidate := range []string{
		"http://127.0.0.1:19790/",
		"http://127.0.0.1:19790",
		"http://127.0.0.1:19790/#token=" + strings.Repeat("0", 64),
		"http://127.0.0.1:19790/?view=yaml",
		"http://127.0.0.1:19790/app.js",
		"http://127.0.0.1:019790/",
	} {
		if !configurationWindowAllows(19790, candidate) {
			t.Fatalf("blocked the panel's own page %q", candidate)
		}
	}
	for _, candidate := range []string{
		"",
		"about:blank",
		"about:srcdoc",
		"data:text/html,<p>",
		"blob:http://127.0.0.1:19790/0d6c0f6e",
		"javascript:alert(1)",
		"file:///etc/hosts",
		"https://example.com/",
		"https://127.0.0.1:19790/",
		"ws://127.0.0.1:19790/",
		"mailto:user@example.com",
		"tailscale://login",
		"http://127.0.0.1:19791/",
		"http://127.0.0.1/",
		"http://127.0.0.1:80/",
		"http://localhost:19790/",
		"http://127.0.0.2:19790/",
		"http://0x7f000001:19790/",
		"http://[::ffff:127.0.0.1]:19790/",
		"http://127.0.0.1:19790@example.com/",
		"http://127.0.0.1.nip.io:19790/",
		"http://127.0.0.1:19790\\example.com",
		"http://127.0.0.1:19790/\t",
	} {
		if configurationWindowAllows(19790, candidate) {
			t.Fatalf("allowed navigation to %q", candidate)
		}
	}
	// WebKit omits the default port, so only a page served on port 80 may lose it.
	if !configurationWindowAllows(80, "http://127.0.0.1/") || !configurationWindowAllows(80, "http://127.0.0.1:80/") {
		t.Fatal("blocked a default-port page")
	}
	if configurationWindowAllows(0, "http://127.0.0.1:0/") {
		t.Fatal("allowed port zero")
	}
}
