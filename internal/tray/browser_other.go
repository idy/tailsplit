//go:build !darwin || !cgo

package tray

func defaultBrowserID() string { return "" }
