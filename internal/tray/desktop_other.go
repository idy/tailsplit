//go:build (!darwin || !cgo) && !windows

package tray

import "errors"

func LoginItem() LoginItemStatus           { return LoginItemUnavailable }
func SetLoginItem(bool) error              { return errors.New("login items require the macOS app") }
func OpenLoginItemSettings()               {}
func ShowAbout()                           {}
func AppVersion() string                   { return "dev" }
func ConfirmUpdate(string) bool            { return false }
func EditDeviceName(string) (string, bool) { return "", false }
