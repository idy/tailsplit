//go:build windows

package desktopwindow

import (
	"golang.org/x/sys/windows"
	"unsafe"
)

var setWindowProperty = windows.NewLazySystemDLL("user32.dll").NewProc("SetPropW")

func restoreNativeFrame(uintptr) {}
func setStartupStage(handle uintptr, stage int, ready bool) {
	phase, _ := windows.UTF16PtrFromString("TailSplit.WebView2.StartupStage")
	loaded, _ := windows.UTF16PtrFromString("TailSplit.WebView2.Ready")
	setWindowProperty.Call(handle, uintptr(unsafe.Pointer(phase)), uintptr(stage))
	value := uintptr(0)
	if ready {
		value = 1
	}
	setWindowProperty.Call(handle, uintptr(unsafe.Pointer(loaded)), value)
}
