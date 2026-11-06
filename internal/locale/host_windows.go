//go:build windows

package locale

import "golang.org/x/sys/windows"

var getUserDefaultUILanguage = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetUserDefaultUILanguage")

func nativeUILanguage() string {
	if err := getUserDefaultUILanguage.Find(); err != nil {
		return ""
	}
	languageID, _, _ := getUserDefaultUILanguage.Call()
	if languageID == 0 {
		return ""
	}
	// PRIMARYLANGID masks off the sublanguage bits, covering simplified,
	// traditional, Hong Kong, Singapore and other Chinese UI variants.
	if languageID&0x3ff == 0x04 {
		return "zh"
	}
	return "en"
}
