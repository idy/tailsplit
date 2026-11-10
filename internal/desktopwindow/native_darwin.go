//go:build darwin && cgo

package desktopwindow

/*
#cgo LDFLAGS: -framework AppKit
#include <stdint.h>
void tailsplit_restore_mygo_frame(uintptr_t handle);
*/
import "C"

func restoreNativeFrame(handle uintptr)  { C.tailsplit_restore_mygo_frame(C.uintptr_t(handle)) }
func setStartupStage(uintptr, int, bool) {}
