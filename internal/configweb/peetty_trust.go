package configweb

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/breml/rootcerts/embedded"
)

// Source authentication has an application-pinned trust anchor: the Mozilla
// public CA bundle in the checksummed rootcerts module. It never merges system
// certificates, SSL_CERT_FILE/SSL_CERT_DIR, or a caller's default transport.
// A locally trusted TLS-intercepting proxy may alter CDN delivery but cannot
// authenticate replacement GitHub tag responses or publisher bytes here.
func newPeettySourceClient() *http.Client {
	roots := x509.NewCertPool()
	// An empty or invalid bundle leaves an empty, rejecting pool, never nil
	// (nil would fall back to the system certificate store).
	roots.AppendCertsFromPEM([]byte(embedded.MozillaCACertificatesPEM()))
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			TLSClientConfig:       &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          10,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("Peetty publisher redirects are not allowed")
		},
	}
}
