//go:build darwin && cgo

package desktopwindow

/*
#cgo LDFLAGS: -framework AppKit
#include <stdint.h>
void tailsplit_restore_mygo_frame(uintptr_t handle);
uint32_t tailsplit_popup_proof_state(uintptr_t handle);
uintptr_t tailsplit_new_popup_proof_target(void);
void tailsplit_click_popup_proof_target(uintptr_t handle);
void tailsplit_close_popup_proof_target(uintptr_t handle);
*/
import "C"

func restoreNativeFrame(handle uintptr)  { C.tailsplit_restore_mygo_frame(C.uintptr_t(handle)) }
func setStartupStage(uintptr, int, bool) {}

func nativePopupProofState(handle uintptr) map[string]bool {
	state := uint32(C.tailsplit_popup_proof_state(C.uintptr_t(handle)))
	result := make(map[string]bool)
	for bit, name := range []string{
		"nonactivating_panel", "joins_all_spaces", "fullscreen_auxiliary",
		"on_active_space", "visible_on_screen", "key_window", "app_active",
	} {
		result[name] = state&(1<<bit) != 0
	}
	return result
}

func newPopupProofTarget() uintptr { return uintptr(C.tailsplit_new_popup_proof_target()) }
func clickPopupProofTarget(handle uintptr) {
	C.tailsplit_click_popup_proof_target(C.uintptr_t(handle))
}
func closePopupProofTarget(handle uintptr) {
	C.tailsplit_close_popup_proof_target(C.uintptr_t(handle))
}
