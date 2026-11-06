//go:build darwin || windows

package main

import "github.com/getlantern/systray"

type menuText struct {
	item           *systray.MenuItem
	title, tooltip string
}

// Accessed only by the native menu event loop. Relabel the same objects so
// language changes retain check states, enablement and click handlers.
var nativeMenuText []menuText

func addMenu(parent *systray.MenuItem, title, tooltip string, checked ...bool) *systray.MenuItem {
	var item *systray.MenuItem
	if parent == nil {
		if len(checked) != 0 {
			item = systray.AddMenuItemCheckbox(tr(title), tr(tooltip), checked[0])
		} else {
			item = systray.AddMenuItem(tr(title), tr(tooltip))
		}
	} else {
		if len(checked) != 0 {
			item = parent.AddSubMenuItemCheckbox(tr(title), tr(tooltip), checked[0])
		} else {
			item = parent.AddSubMenuItem(tr(title), tr(tooltip))
		}
	}
	nativeMenuText = append(nativeMenuText, menuText{item, title, tooltip})
	return item
}

func refreshMenuLanguage() {
	for _, text := range nativeMenuText {
		text.item.SetTitle(tr(text.title))
		text.item.SetTooltip(tr(text.tooltip))
	}
}
