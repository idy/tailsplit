package desktoptray

import (
	"errors"
	"fmt"
	"log"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/egoist/mygo"
)

var runState struct {
	sync.Mutex
	started bool
}

// Run must be called on the main goroutine. The native tray is created in
// WhenReady, then onReady runs on its own goroutine: TailSplit's Windows
// onReady callback is itself a blocking menu event loop. onExit runs exactly
// once, including when MyGo cannot initialize or cannot create the tray.
func Run(onReady, onExit func()) error {
	runState.Lock()
	if runState.started {
		runState.Unlock()
		return errors.New("desktoptray: Run called twice")
	}
	runState.started = true
	runState.Unlock()

	stop := make(chan struct{})
	var workerDone chan struct{}
	var exitOnce sync.Once
	finish := func() {
		exitOnce.Do(func() {
			defaultState.mu.Lock()
			defaultState.stopped = true
			defaultState.mu.Unlock()
			close(stop)
			if onExit != nil {
				onExit()
			}
		})
	}
	defer func() {
		finish()
		if workerDone != nil {
			<-workerDone
		}
	}()

	mygo.App.SetName("TailSplit")
	off := mygo.App.OnQuit(finish)
	defer off()
	// Finish network teardown while the native loop is still alive. MyGo's
	// normal quit sequence has a short exit deadline after closing windows.
	// Native Quit / Cmd-Q must wait for the same cleanup as the tray command.
	var exitReady atomic.Bool
	var exitStarted sync.Once
	offBefore := mygo.App.OnBeforeQuit(func(event *mygo.QuitEvent) {
		if exitReady.Load() {
			return
		}
		event.PreventDefault()
		exitStarted.Do(func() {
			go func() {
				finish()
				exitReady.Store(true)
				mygo.App.Quit()
			}()
		})
	})
	defer offBefore()
	var trayErr error // WhenReady and App.Run's return both run on main.
	mygo.App.WhenReady(func() {
		// Consume the preseeded icon and model before creating the native tray.
		snap := defaultState.takeSnapshot(allChanged)
		menu := newMenuRenderer(nil)
		menu.apply(snap.items)
		tray, err := mygo.NewTray(mygo.TrayOptions{
			ID: "TailSplit", Icon: snap.icon, IconIsTemplate: snap.template,
			ToolTip: snap.tooltip, Menu: menu.menu,
		})
		if err != nil {
			trayErr = fmt.Errorf("desktoptray: create tray: %w", err)
			mygo.App.Quit()
			return
		}
		menu.setMenu = tray.SetMenu
		configurePrimaryClick(runtime.GOOS, tray)
		workerDone = make(chan struct{})
		go func() {
			defer close(workerDone)
			defaultState.renderLoop(stop, func(snap traySnapshot) {
				// MyGo's Tray.SetMenu stores its menu before dispatching. Keep
				// that write, popup reads, and all native item access on the UI
				// thread, after releasing the model lock.
				mygo.RunOnMain(func() {
					if snap.changed&iconChanged != 0 && len(snap.icon) != 0 {
						if err := tray.SetIcon(snap.icon, snap.template); err != nil {
							log.Printf("desktoptray: set icon: %v", err)
						}
					}
					if snap.changed&tooltipChanged != 0 {
						tray.SetToolTip(snap.tooltip)
					}
					if snap.changed&menuChanged != 0 {
						menu.apply(snap.items)
					}
				})
			})
		}()
		if onReady != nil {
			go onReady()
		}
	})
	return errors.Join(mygo.App.Run(), trayErr)
}

// Quit requests the normal MyGo application quit sequence.
func Quit() { mygo.App.Quit() }

// SetIcon accepts PNG artwork on both macOS and Windows.
// Invalid images leave the previous icon in place, matching this void API.
func SetIcon(icon []byte) { setIcon(defaultState, icon, false) }

// SetTemplateIcon uses macOS's template PNG and the regular artwork elsewhere.
func SetTemplateIcon(templateIcon, regularIcon []byte) {
	if runtime.GOOS == "darwin" {
		setIcon(defaultState, templateIcon, true)
	} else {
		setIcon(defaultState, regularIcon, false)
	}
}

func setIcon(s *menuState, icon []byte, template bool) {
	png, err := normalizeIcon(icon)
	if err != nil {
		log.Printf("desktoptray: set icon: %v", err)
		return
	}
	if runtime.GOOS == "darwin" {
		png, err = macTrayIcon(png)
		if err != nil {
			log.Printf("desktoptray: prepare macOS icon: %v", err)
			return
		}
	}
	s.mu.Lock()
	s.icon, s.template = png, template
	s.changedLocked(iconChanged)
	s.mu.Unlock()
}

// SetTooltip sets the tray icon's hover text.
func SetTooltip(tooltip string) {
	defaultState.mu.Lock()
	defer defaultState.mu.Unlock()
	if defaultState.tooltip != tooltip {
		defaultState.tooltip = tooltip
		defaultState.changedLocked(tooltipChanged)
	}
}

type primaryClickTray interface {
	OnClick(func()) func()
	PopUpMenu()
}

func configurePrimaryClick(goos string, tray primaryClickTray) {
	// MyGo v0.8.3 opens Windows tray menus only for right clicks. macOS
	// already opens the assigned menu for either button.
	if goos == "windows" {
		tray.OnClick(tray.PopUpMenu)
	}
}

// menuRenderer and its MyGo objects are confined to the UI thread. Immutable
// model snapshots cross that boundary; the model lock never crosses it. Keep
// one strong native reference per logical item: MyGo assigns a private UID to
// each pointer and holds only a weak reference in its callback registry. In
// particular, replacing a tray menu cannot retire items in an open Windows
// popup, whose native commands still refer to those UIDs.
//
// Menus only append or hide items, so this cache is bounded by the live model.
// Rebuilds reuse every item pointer and retain no history of old menu trees.
// Scalar refreshes walk snapshots directly without building a second tree.
// No GUI event loop is needed to construct or exercise this renderer in tests.
type menuRenderer struct {
	menu    *mygo.Menu
	roots   []*mygo.MenuItem
	items   map[*MenuItem]*renderedItem
	setMenu func(*mygo.Menu)
}

type renderedItem struct {
	native *mygo.MenuItem
	last   itemSnapshot // scalar fields only; children are not retained
}

func newMenuRenderer(setMenu func(*mygo.Menu)) *menuRenderer {
	return &menuRenderer{items: make(map[*MenuItem]*renderedItem), setMenu: setMenu}
}

// apply reports whether the final batched state caused a native update.
func (r *menuRenderer) apply(items []itemSnapshot) bool {
	if r.menu == nil || !r.sameStructure(items, r.roots) {
		// Build the entire final template before registering/snapshotting it.
		// Posting scalar updates while constructing submenus would let their
		// older snapshots overwrite the new structure after SetMenu returns.
		r.roots = r.buildItems(items)
		r.menu = mygo.NewMenu(r.roots)
		if r.setMenu != nil {
			r.setMenu(r.menu)
		}
		return true
	}
	return r.updateItems(items)
}

func nativeKind(snap itemSnapshot) mygo.MenuItemType {
	switch {
	case snap.separator:
		return mygo.MenuItemSeparator
	case snap.checkbox || snap.checked:
		return mygo.MenuItemCheckbox
	case len(snap.children) != 0:
		return mygo.MenuItemSubmenu
	default:
		return mygo.MenuItemNormal
	}
}

func (r *menuRenderer) sameStructure(items []itemSnapshot, native []*mygo.MenuItem) bool {
	if len(items) != len(native) {
		return false
	}
	for i, snap := range items {
		item := r.items[snap.item]
		if item == nil || item.native != native[i] || nativeKind(snap) != item.native.Type ||
			!r.sameStructure(snap.children, item.native.Submenu) {
			return false
		}
	}
	return true
}

func (r *menuRenderer) buildItems(items []itemSnapshot) []*mygo.MenuItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]*mygo.MenuItem, len(items))
	for i, snap := range items {
		item := r.items[snap.item]
		if item == nil {
			model := snap.item
			item = &renderedItem{native: &mygo.MenuItem{ID: snap.id}}
			item.native.Click = func(native *mygo.MenuItem, _ *mygo.Window) {
				if native.Type == mygo.MenuItemCheckbox {
					model.clicked(native.SetChecked)
				} else {
					model.clicked(nil)
				}
			}
			r.items[model] = item
		}
		native := item.native
		native.Label, native.ToolTip, native.Type = snap.title, snap.tooltip, nativeKind(snap)
		native.Disabled, native.Hidden, native.Checked = snap.disabled, snap.hidden, snap.checked
		native.Submenu = r.buildItems(snap.children)
		item.last = snap
		item.last.children = nil
		out[i] = native
	}
	return out
}

func (r *menuRenderer) updateItems(items []itemSnapshot) bool {
	changed := false
	for _, snap := range items {
		item := r.items[snap.item]
		native, last := item.native, item.last
		tipChanged := last.tooltip != snap.tooltip
		if tipChanged {
			// v0.8.3 has no MenuItem.SetToolTip. This private item is only
			// read or changed on the UI thread, including SDK callbacks and
			// snapshots. SetLabel below publishes the new tooltip using the
			// SDK's normal UpdateMenuItem path, without rebuilding NSMenu.
			native.ToolTip = snap.tooltip
		}
		if last.title != snap.title || tipChanged {
			native.SetLabel(snap.title)
			changed = true
		}
		if last.disabled != snap.disabled {
			native.SetEnabled(!snap.disabled)
			changed = true
		}
		if last.hidden != snap.hidden {
			native.SetVisible(!snap.hidden)
			changed = true
		}
		if last.checked != snap.checked {
			native.SetChecked(snap.checked)
			changed = true
		}
		item.last = snap
		item.last.children = nil
		if r.updateItems(snap.children) {
			changed = true
		}
	}
	return changed
}
