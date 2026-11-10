package desktoptray

import (
	"testing"

	"github.com/egoist/mygo"
)

type popupTestTray struct {
	left, right func()
	menu        *mygo.Menu
	shown       []*mygo.Menu
	installed   []*mygo.Menu
}

func (t *popupTestTray) OnClick(fn func()) func()      { t.left = fn; return func() {} }
func (t *popupTestTray) OnRightClick(fn func()) func() { t.right = fn; return func() {} }
func (t *popupTestTray) SetMenu(menu *mygo.Menu) {
	t.menu = menu
	t.installed = append(t.installed, menu)
}
func (t *popupTestTray) PopUpMenu() { t.shown = append(t.shown, t.menu) }

func TestPopupClickRoutingKeepsUpdatedMenuDetached(t *testing.T) {
	model := newMenuState()
	model.add(nil, "Open TailSplit", "", false, false, false)
	renderer := newMenuRenderer(nil)
	renderer.apply(model.takeSnapshot(allChanged).items)
	tray := &popupTestTray{}
	toggles, closes := 0, 0
	var failure func()
	configurePopupClicks(tray, renderer, func(fallback func()) {
		toggles++
		failure = fallback
	}, func() { closes++ })
	tray.left()
	if toggles != 1 || closes != 0 || len(tray.shown) != 0 || len(tray.installed) != 0 {
		t.Fatal("left click displayed or attached the native menu")
	}

	firstMenu := renderer.menu
	model.add(nil, "Quit", "", false, false, false)
	renderer.apply(model.takeSnapshot(0).items)
	if renderer.menu == firstMenu || len(tray.installed) != 0 {
		t.Fatal("structural menu refresh attached a permanent menu")
	}
	tray.right()
	failure()
	if closes != 2 || len(tray.shown) != 2 || tray.shown[0] != renderer.menu || tray.shown[1] != renderer.menu {
		t.Fatal("right click/failure did not close popup and use latest menu")
	}
	if tray.menu != nil || len(tray.installed) != 4 || tray.installed[1] != nil || tray.installed[3] != nil {
		t.Fatal("temporary native menu remained attached")
	}
	tray.left()
	if toggles != 2 || len(tray.shown) != 2 {
		t.Fatal("menu fallback disabled later popup clicks")
	}
}

func TestSetPopupURLBeforeRun(t *testing.T) {
	// Do not start a native application: this API only validates/stores settings.
	runState.Lock()
	started, popup := runState.started, runState.popup
	runState.started, runState.popup = false, nil
	runState.Unlock()
	t.Cleanup(func() {
		runState.Lock()
		runState.started, runState.popup = started, popup
		runState.Unlock()
	})
	SetPopupURL("http://127.0.0.1:43821/tray.html#capability")
	if runState.popup == nil {
		t.Fatal("valid popup was not configured")
	}
	SetPopupURL("https://example.com/tray.html#capability")
	if runState.popup != nil {
		t.Fatal("invalid URL did not restore regular-menu behavior")
	}
	SetPopupURL("http://127.0.0.1:43821/tray.html#capability")
	SetPopupURL("")
	if runState.popup != nil {
		t.Fatal("empty URL did not restore regular-menu behavior")
	}
	SetPopupURL("http://127.0.0.1:43821/tray.html#capability")
	configured := runState.popup
	runState.started = true
	SetPopupURL("")
	if runState.popup != configured {
		t.Fatal("late configuration replaced the running popup")
	}
}
