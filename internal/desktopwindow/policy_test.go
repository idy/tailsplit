package desktopwindow

import (
	"testing"

	"github.com/egoist/mygo"
)

type recordedPagePolicy struct {
	navigate   func(*mygo.NavigateEvent)
	openWindow func(mygo.WindowOpenRequest) *mygo.WindowOptions
	context    func(*mygo.ContextMenuEvent)
	permission func(mygo.PermissionRequest) bool
	download   func(*mygo.DownloadEvent)
}

func (p *recordedPagePolicy) OnWillNavigate(fn func(*mygo.NavigateEvent)) func() {
	p.navigate = fn
	return nil
}
func (p *recordedPagePolicy) SetWindowOpenHandler(fn func(mygo.WindowOpenRequest) *mygo.WindowOptions) {
	p.openWindow = fn
}
func (p *recordedPagePolicy) OnContextMenu(fn func(*mygo.ContextMenuEvent)) func() {
	p.context = fn
	return nil
}
func (p *recordedPagePolicy) SetPermissionHandler(fn func(mygo.PermissionRequest) bool) {
	p.permission = fn
}
func (p *recordedPagePolicy) OnWillDownload(fn func(*mygo.DownloadEvent)) func() {
	p.download = fn
	return nil
}

func TestConfigurationPagePolicy(t *testing.T) {
	page := &recordedPagePolicy{}
	configurePagePolicy(page, 43821)
	if page.navigate == nil || page.openWindow == nil || page.context == nil || page.permission == nil || page.download == nil {
		t.Fatal("configuration page policy is incomplete")
	}
	for _, test := range []struct {
		url     string
		allowed bool
	}{
		{"http://127.0.0.1:43821/#capability", true},
		{"http://127.0.0.1:43821/settings", true},
		{"http://127.0.0.1:43822/", false},
		{"http://localhost:43821/", false},
		{"http://127.0.0.1:43821@other.example/", false},
		{"http://127.0.0.1:43821\\@other.example/", false},
		{"https://example.com/", false},
		{"file:///tmp/panel.html", false},
	} {
		event := &mygo.NavigateEvent{URL: test.url}
		page.navigate(event)
		if event.DefaultPrevented() == test.allowed {
			t.Errorf("navigation to %q: prevented = %t", test.url, event.DefaultPrevented())
		}
		if page.openWindow(mygo.WindowOpenRequest{URL: test.url, UserInitiated: true}) != nil {
			t.Errorf("popup allowed for %q", test.url)
		}
	}
	for _, permission := range []mygo.Permission{mygo.PermissionCamera, mygo.PermissionMicrophone, mygo.PermissionGeolocation, mygo.PermissionNotifications, "unknown"} {
		if page.permission(mygo.PermissionRequest{Permissions: []mygo.Permission{permission}, Origin: "http://127.0.0.1:43821"}) {
			t.Errorf("permission %q was granted to the trusted panel", permission)
		}
	}
	menu := &mygo.ContextMenuEvent{}
	page.context(menu)
	if !menu.DefaultPrevented() {
		t.Fatal("context menu was allowed")
	}
	download := &mygo.DownloadEvent{URL: "http://127.0.0.1:43821/export", SuggestedName: "export.txt"}
	page.download(download)
	if !download.DefaultPrevented() {
		t.Fatal("download was allowed")
	}
}
