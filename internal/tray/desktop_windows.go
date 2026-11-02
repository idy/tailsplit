//go:build windows

package tray

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const startupKey = `Software\Microsoft\Windows\CurrentVersion\Run`

func LoginItem() LoginItemStatus {
	key, err := registry.OpenKey(registry.CURRENT_USER, startupKey, registry.QUERY_VALUE)
	if err != nil {
		return LoginItemDisabled
	}
	defer key.Close()
	value, _, err := key.GetStringValue("TailSplit")
	executable, pathErr := os.Executable()
	if err == nil && pathErr == nil && value == windows.EscapeArg(executable)+" --background" {
		return LoginItemEnabled
	}
	return LoginItemDisabled
}

func SetLoginItem(enabled bool) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, startupKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if !enabled {
		err := key.DeleteValue("TailSplit")
		if err == registry.ErrNotExist {
			return nil
		}
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	return key.SetStringValue("TailSplit", windows.EscapeArg(filepath.Clean(executable))+" --background")
}

func OpenLoginItemSettings()               {}
func ShowAbout()                           {}
func EditDeviceName(string) (string, bool) { return "", false }
