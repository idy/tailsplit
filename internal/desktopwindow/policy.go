package desktopwindow

import (
	"net/url"
	"strings"

	"github.com/egoist/mygo"
	"github.com/idy/tailsplit/internal/tray"
)

// normalizePageOrigin matches location.origin's omission of the default HTTP
// port. Preserve every other authority byte; aliases and prefix matches must not
// expand the panel's origin boundary. Invalid origins cannot match a browser.
func normalizePageOrigin(origin string) string {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return ""
	}
	if parsed.Scheme == "http" && parsed.Port() == "80" {
		parsed.Host = strings.TrimSuffix(parsed.Host, ":80")
	}
	return parsed.Scheme + "://" + parsed.Host
}

type policyPage interface {
	OnWillNavigate(func(*mygo.NavigateEvent)) func()
	SetWindowOpenHandler(func(mygo.WindowOpenRequest) *mygo.WindowOptions)
	OnContextMenu(func(*mygo.ContextMenuEvent)) func()
	SetPermissionHandler(func(mygo.PermissionRequest) bool)
	OnWillDownload(func(*mygo.DownloadEvent)) func()
}

func configurePagePolicy(page policyPage, port uint16) {
	page.OnWillNavigate(func(event *mygo.NavigateEvent) {
		candidate, err := url.Parse(event.URL)
		if err != nil || candidate.Path == "/native-cancel-arm" || !tray.ConfigurationWindowAllows(port, event.URL) {
			event.PreventDefault()
		}
	})
	page.SetWindowOpenHandler(func(mygo.WindowOpenRequest) *mygo.WindowOptions { return nil })
	page.OnContextMenu(func(event *mygo.ContextMenuEvent) { event.PreventDefault() })
	// MyGo v0.8.3 forwards camera, microphone, geolocation and notification
	// requests on Windows; other WebView2 permission kinds are not exposed.
	page.SetPermissionHandler(func(mygo.PermissionRequest) bool { return false })
	page.OnWillDownload(func(event *mygo.DownloadEvent) { event.PreventDefault() })
}
