//go:build windows

package main

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf16"
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

func launchWindowsUpdate(update appupdate.Package) error {
	verb, _ := windows.UTF16PtrFromString("open")
	file, err := windows.UTF16PtrFromString(update.Path)
	if err != nil {
		return err
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	parameters, _ := windows.UTF16PtrFromString("/SP- /NORESTART /UserSID=" + user.User.Sid.String())
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
	defer update.Cleanup()
	// Setup exits this app before replacing it. A detached ordinary-user cleanup
	// process survives that exit and removes the private download after Setup.
	if pid, err := windows.GetProcessId(info.Process); err == nil {
		startWindowsUpdateCleanup(pid, update.Directory)
	}
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

func startWindowsUpdateCleanup(pid uint32, directory string) {
	system, err := windows.GetSystemDirectory()
	if err != nil {
		return
	}
	literal := "'" + strings.ReplaceAll(directory, "'", "''") + "'"
	script := "$p=Get-Process -Id " + strconv.FormatUint(uint64(pid), 10) + " -ErrorAction SilentlyContinue; if($p){try{$p.WaitForExit()}catch{exit 1}}; Remove-Item -LiteralPath " + literal + " -Recurse -Force -ErrorAction SilentlyContinue"
	units := utf16.Encode([]rune(script))
	encoded := make([]byte, 2*len(units))
	for i, unit := range units {
		binary.LittleEndian.PutUint16(encoded[2*i:], unit)
	}
	command := exec.Command(system+`\WindowsPowerShell\v1.0\powershell.exe`, "-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(encoded))
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP}
	if command.Start() == nil {
		_ = command.Process.Release()
	}
}
