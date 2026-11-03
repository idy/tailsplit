//go:build windows

package tray

import (
	"fmt"
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

func ConfirmUpdate(version string) bool {
	text, _ := windows.UTF16PtrFromString(fmt.Sprintf("升级到 TailSplit %s？\n\n将下载并校验安装包，然后打开安装向导。安装时会断开连接；配置和设备登录会保留。Windows 将请求管理员批准。", version))
	title, _ := windows.UTF16PtrFromString("TailSplit 更新")
	answer, _ := windows.MessageBox(0, text, title, windows.MB_YESNO|windows.MB_ICONQUESTION|windows.MB_DEFBUTTON2|windows.MB_SETFOREGROUND)
	return answer == 6 // IDYES
}

func OpenLoginItemSettings()               {}
func ShowAbout()                           {}
func EditDeviceName(string) (string, bool) { return "", false }
