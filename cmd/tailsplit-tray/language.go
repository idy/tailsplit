package main

import (
	"github.com/idy/tailsplit/internal/locale"
	"github.com/idy/tailsplit/internal/tray"
)

// Initialized once before native menus or configuration handlers are created.
var uiLanguage *locale.Store

func currentLanguage() string {
	if uiLanguage == nil {
		return "en"
	}
	return uiLanguage.Get().Language
}

func tr(text string) string { return tray.Translate(currentLanguage(), text) }
