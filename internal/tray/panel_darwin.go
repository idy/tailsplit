//go:build darwin && cgo

package tray

/*
#cgo LDFLAGS: -framework AppKit -framework WebKit
#include <stdlib.h>
int tailsplit_show_configuration_window(const char *url, int port);
*/
import "C"

import (
	"errors"
	"unsafe"
)

// ShowConfigurationWindow shows the app's configuration window and creates it
// for rawURL on first use. Closing the window only hides it, so later calls
// return to the same page and any unsaved draft. An error means the window
// could not be shown; the caller may open rawURL in a browser instead.
func ShowConfigurationWindow(rawURL string) error {
	port, err := configurationWindowPort(rawURL)
	if err != nil {
		return err
	}
	value := C.CString(rawURL)
	defer C.free(unsafe.Pointer(value))
	switch C.tailsplit_show_configuration_window(value, C.int(port)) {
	case 0:
		return nil
	case 2:
		return errors.New("the configuration window already shows another page")
	default:
		return errors.New("could not create the configuration window")
	}
}

// WebKit asks this on the main thread for every navigation and response.
//
//export tailsplitConfigurationWindowAllows
func tailsplitConfigurationWindowAllows(url *C.char, port C.int) C.int {
	if url != nil && port > 0 && port <= 65535 && configurationWindowAllows(uint16(port), C.GoString(url)) {
		return 1
	}
	return 0
}
