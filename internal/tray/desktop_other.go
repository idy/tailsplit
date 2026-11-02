//go:build !darwin || !cgo

package tray

import "errors"

func LoginItem() LoginItemStatus           { return LoginItemUnavailable }
func SetLoginItem(bool) error              { return errors.New("login items require the macOS app") }
func OpenLoginItemSettings()               {}
func ShowAbout()                           {}
func EditDeviceName(string) (string, bool) { return "", false }
