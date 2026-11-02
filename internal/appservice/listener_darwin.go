//go:build darwin

package appservice

/*
#include <launch.h>
#include <stdlib.h>
*/
import "C"

import (
	"errors"
	"net"
	"os"
	"syscall"
	"unsafe"
)

func activatedListener() (net.Listener, error) {
	name := C.CString("Control")
	defer C.free(unsafe.Pointer(name))
	var descriptors *C.int
	var count C.size_t
	if result := C.launch_activate_socket(name, &descriptors, &count); result != 0 {
		return nil, syscall.Errno(result)
	}
	defer C.free(unsafe.Pointer(descriptors))
	fds := unsafe.Slice(descriptors, int(count))
	if len(fds) != 1 {
		for _, fd := range fds {
			_ = syscall.Close(int(fd))
		}
		return nil, errors.New("launchd did not supply the network service control socket")
	}
	file := os.NewFile(uintptr(fds[0]), "Control")
	defer file.Close()
	return net.FileListener(file)
}
