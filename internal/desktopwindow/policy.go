package desktopwindow

import (
	"github.com/egoist/mygo"
	"github.com/idy/tailsplit/internal/tray"
)

type policyPage interface {
	OnWillNavigate(func(*mygo.NavigateEvent)) func()
	SetWindowOpenHandler(func(mygo.WindowOpenRequest) *mygo.WindowOptions)
	OnContextMenu(func(*mygo.ContextMenuEvent)) func()
	SetPermissionHandler(func(mygo.PermissionRequest) bool)
	OnWillDownload(func(*mygo.DownloadEvent)) func()
}

func configurePagePolicy(page policyPage, port uint16) {
	page.OnWillNavigate(func(event *mygo.NavigateEvent) {
		if !tray.ConfigurationWindowAllows(port, event.URL) {
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
