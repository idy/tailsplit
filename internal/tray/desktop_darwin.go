//go:build darwin && cgo

package tray

/*
#cgo LDFLAGS: -framework AppKit -framework ServiceManagement
#include <stdlib.h>
void tailsplit_set_ui_language(int chinese);
int tailsplit_login_item_status(void);
char *tailsplit_set_login_item(int enabled);
void tailsplit_login_item_settings(void);
void tailsplit_show_about(void);
char *tailsplit_app_version(void);
int tailsplit_confirm_update(const char *version);
char *tailsplit_edit_device_name(const char *current, const char *suggested, int *saved);
*/
import "C"

import (
	"errors"
	"unsafe"
)

func LoginItem() LoginItemStatus { return LoginItemStatus(C.tailsplit_login_item_status()) }

func SetLoginItem(enabled bool) error {
	var flag C.int
	if enabled {
		flag = 1
	}
	message := C.tailsplit_set_login_item(flag)
	if message == nil {
		return nil
	}
	defer C.free(unsafe.Pointer(message))
	return errors.New(C.GoString(message))
}

func OpenLoginItemSettings() { C.tailsplit_login_item_settings() }

func ShowAbout() { C.tailsplit_show_about() }

func AppVersion() string {
	value := C.tailsplit_app_version()
	defer C.free(unsafe.Pointer(value))
	return C.GoString(value)
}

func ConfirmUpdate(version string) bool {
	value := C.CString(version)
	defer C.free(unsafe.Pointer(value))
	return C.tailsplit_confirm_update(value) != 0
}

func EditDeviceName(current string) (string, bool) {
	suggested, _ := DeviceName("")
	input, placeholder := C.CString(current), C.CString(suggested)
	defer C.free(unsafe.Pointer(input))
	defer C.free(unsafe.Pointer(placeholder))
	var saved C.int
	value := C.tailsplit_edit_device_name(input, placeholder, &saved)
	if value == nil {
		return "", false
	}
	defer C.free(unsafe.Pointer(value))
	return C.GoString(value), saved != 0
}

func setNativeUILanguage(language string) {
	var chinese C.int
	if language == "zh" {
		chinese = 1
	}
	C.tailsplit_set_ui_language(chinese)
}
