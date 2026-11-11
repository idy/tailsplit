package configweb

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestPeettySourceRejectsCertificatesTrustedByDeliveryClient(t *testing.T) {
	proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "forged publisher and CDN content")
	}))
	proxy.Config.ErrorLog = log.New(io.Discard, "", 0)
	proxy.StartTLS()
	defer proxy.Close()

	// Model a TLS-intercepting proxy whose private certificate is accepted by
	// the delivery/system trust configuration, without changing the OS store.
	delivery := proxy.Client()
	response, err := delivery.Get(proxy.URL)
	if err != nil {
		t.Fatal("delivery did not trust the proxy certificate:", err)
	}
	response.Body.Close()

	publisher := newPeettySourceClient()
	defer publisher.CloseIdleConnections()
	transport := publisher.Transport.(*http.Transport)
	transport.Proxy = nil
	if transport.TLSClientConfig.RootCAs == nil || len(transport.TLSClientConfig.RootCAs.Subjects()) == 0 || transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("publisher client must have explicit, populated application roots")
	}
	_, err = publisher.Get(proxy.URL)
	var unknownAuthority x509.UnknownAuthorityError
	if !errors.As(err, &unknownAuthority) {
		t.Fatalf("publisher accepted shared proxy trust or failed for the wrong reason: %v", err)
	}
}

func TestPeettyPublisherRequestsNeverUseDeliveryTrust(t *testing.T) {
	u := newPeettyUpdates(filepath.Join(t.TempDir(), "config.yaml"))
	defer u.sourceClient.CloseIdleConnections()
	var deliveryCalls int
	u.client = &http.Client{Transport: peettyTransport(func(_ *http.Request) (*http.Response, error) {
		deliveryCalls++
		return nil, errors.New("unexpected use of delivery trust")
	})}
	sentinel := errors.New("independent publisher verifier")
	u.sourceClient.Transport = peettyTransport(func(_ *http.Request) (*http.Response, error) { return nil, sentinel })
	for _, source := range []string{peettyGitHubAPI + "/git/ref/tags/v0.3.1", peettySourceBase + peettyTestCommit("v0.3.1") + "/cdn/peetty.global.js"} {
		_, err := u.fetch(context.Background(), source, 1024)
		if !errors.Is(err, sentinel) {
			t.Fatal("publisher request bypassed its independent verifier:", err)
		}
	}
	if deliveryCalls != 0 {
		t.Fatal("publisher requests used the CDN client")
	}
	u.sourceClient = nil
	if _, err := u.fetch(context.Background(), peettyGitHubAPI+"/git/ref/tags/v0.3.1", 1024); err == nil || deliveryCalls != 0 {
		t.Fatal("missing source verifier fell back to delivery trust")
	}
}

func TestPeettyRejectsCacheEstablishedUsingSystemTLSRoots(t *testing.T) {
	config := filepath.Join(t.TempDir(), "config.yaml")
	cdn := &peettyCDNFixture{versions: []string{"0.3.1"}, files: map[string]map[string][]byte{"v0.3.1": peettyTestFiles("red", false)}}
	u := newPeettyUpdates(config)
	cdn.attach(t, u)
	if err := u.check(context.Background()); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(u.path)
	var cache peettyRelease
	if err := json.Unmarshal(data, &cache); err != nil {
		t.Fatal(err)
	}
	cache.Format = 2 // The prior source verifier used the system trust store.
	data, _ = json.Marshal(cache)
	if err := os.WriteFile(u.path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if newPeettyUpdates(config).snapshot() != nil {
		t.Fatal("old shared-TLS cache was trusted")
	}
}
