//go:build windows

package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"github.com/idy/tailsplit/internal/appupdate"
	"golang.org/x/sys/windows"
)

// ShellExecuteEx retains the installer process handle. Setup requests elevation
// itself so its bootstrapper can retain the original user for postinstall launch.
// Setup owns --quit and service cleanup; cancelling leaves the desktop usable.
type updateShellExecuteInfo struct {
	Size       uint32
	Mask       uint32
	Window     windows.Handle
	Verb       *uint16
	File       *uint16
	Parameters *uint16
	Directory  *uint16
	Show       int32
	Instance   windows.Handle
	IDList     unsafe.Pointer
	Class      *uint16
	ClassKey   windows.Handle
	HotKey     uint32
	Icon       windows.Handle
	Process    windows.Handle
}

func launchWindowsUpdate(update appupdate.Package, revision, config, log string) error {
	if !validDesktopUpdateRevision(revision) {
		return fmt.Errorf("invalid update configuration revision")
	}
	verb, _ := windows.UTF16PtrFromString("open")
	file, err := windows.UTF16PtrFromString(update.Path)
	if err != nil {
		return err
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	config, err = filepath.Abs(config)
	if err != nil {
		return err
	}
	log, err = filepath.Abs(log)
	if err != nil {
		return err
	}
	arguments := []string{"/SP-", "/NORESTART", "/RestoreConnection=1", "/UpdateRevision=" + revision,
		"/UpdateConfig=" + config, "/UpdateLog=" + log, "/UserSID=" + user.User.Sid.String()}
	for i := range arguments {
		arguments[i] = windows.EscapeArg(arguments[i])
	}
	parameters, _ := windows.UTF16PtrFromString(strings.Join(arguments, " "))
	info := updateShellExecuteInfo{
		Mask: 0x40 | 0x100, // SEE_MASK_NOCLOSEPROCESS | SEE_MASK_NOASYNC
		Verb: verb, File: file, Parameters: parameters, Show: windows.SW_SHOWNORMAL,
	}
	info.Size = uint32(unsafe.Sizeof(info))
	ok, _, callErr := windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW").Call(uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		if callErr == syscall.Errno(0) {
			return fmt.Errorf("could not open the update installer")
		}
		return fmt.Errorf("could not open the update installer: %w", callErr)
	}
	if info.Process == 0 {
		return fmt.Errorf("the update installer did not return a process handle")
	}
	defer windows.CloseHandle(info.Process)
	// Setup quits this app before replacing it, so this only runs when Setup
	// fails or is cancelled. The next launch removes a download Setup used.
	defer update.Cleanup()
	_, err = windows.WaitForSingleObject(info.Process, windows.INFINITE)
	if err != nil {
		return err
	}
	var code uint32
	if err := windows.GetExitCodeProcess(info.Process, &code); err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("update installer exited with code %d; check the installer message before retrying", code)
	}
	return nil
}
