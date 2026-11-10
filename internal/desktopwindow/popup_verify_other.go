//go:build !darwin || !cgo

package desktopwindow

// App-local outside-click injection is currently available only on AppKit.
func newPopupProofTarget() uintptr  { return 0 }
func clickPopupProofTarget(uintptr) {}
func closePopupProofTarget(uintptr) {}
