//go:build (!darwin || !cgo) && !windows

package desktopwindow

func restoreNativeFrame(uintptr)         {}
func setStartupStage(uintptr, int, bool) {}
