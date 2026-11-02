//go:build darwin && cgo

package tray

/*
#cgo LDFLAGS: -framework AppKit
#include <stdlib.h>
char *tailsplit_default_browser(void);
*/
import "C"

import "unsafe"

func defaultBrowserID() string {
	value := C.tailsplit_default_browser()
	if value == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(value))
	return C.GoString(value)
}
