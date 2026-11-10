//go:build darwin || windows

package main

import systray "github.com/idy/tailsplit/internal/desktoptray"

type menuText struct {
	item           *systray.MenuItem
	title, tooltip string
}

// Accessed only by the native menu event loop. Relabel the same objects so
// language changes retain enablement and click handlers.
var nativeMenuText []menuText

func addMenu(title, tooltip string) *systray.MenuItem {
	item := systray.AddMenuItem(tr(title), tr(tooltip))
	nativeMenuText = append(nativeMenuText, menuText{item, title, tooltip})
	return item
}

func refreshMenuLanguage() {
	for _, text := range nativeMenuText {
		text.item.SetTitle(tr(text.title))
		text.item.SetTooltip(tr(text.tooltip))
	}
}
