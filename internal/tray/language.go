package tray

import (
	"strings"
	"sync/atomic"
)

var uiChinese atomic.Bool

// SetUILanguage changes presentation only; callers retain menus and login flows.
func SetUILanguage(language string) {
	uiChinese.Store(language == "zh")
	setNativeUILanguage(language)
}

func UILanguage() string {
	if uiChinese.Load() {
		return "zh"
	}
	return "en"
}

// Translate translates fixed application strings, leaving external values intact.
func Translate(language, text string) string {
	if language == "zh" {
		return text
	}
	if value, ok := englishUI[text]; ok {
		return value
	}
	// Only known application error prefixes are translated. The wrapped OS or
	// service diagnostic remains byte-for-byte intact.
	for key, value := range englishUI {
		if strings.HasPrefix(text, key+": ") {
			return value + strings.TrimPrefix(text, key)
		}
	}
	return text
}

var englishUI = map[string]string{
	"请从已安装的 TailSplit.app 升级":               "Update from the installed TailSplit.app",
	"请先将 TailSplit.app 移到 Applications 再升级": "Move TailSplit.app to Applications before updating",
	"App 更新目标不能是符号链接":                       "The app update target cannot be a symbolic link",
	"新版 App 的签名校验失败":                        "The new app’s signature verification failed",
	"新版 App 未通过 macOS Gatekeeper 校验":        "The new app did not pass macOS Gatekeeper verification",
	"无法解压更新":                                "Could not extract the update",
	"更新包的 App 标识不正确":                        "The update package has an incorrect app identifier",
	"更新包内版本与 Release 不一致":                   "The update package version does not match the release",
	"无法准备 App 替换（可能取消了管理员授权）":               "Could not prepare app replacement (administrator approval may have been cancelled)",
	"启动失败，请查看日志":                            "Could not start; view logs",
	"代理已退出，请查看日志":                           "The proxy exited; view logs",
	"退出设备登录失败，请查看日志":                        "Could not sign out this device; view logs",
	"后台网络服务准备失败，请重试":                        "Could not prepare the network service; try again",

	"隐藏 TailSplit": "Hide TailSplit",
	"隐藏其他":         "Hide Others",
	"全部显示":         "Show All",
	"编辑":           "Edit",
	"撤销":           "Undo",
	"重做":           "Redo",
	"剪切":           "Cut",
	"拷贝":           "Copy",
	"粘贴":           "Paste",
	"粘贴并匹配样式":      "Paste and Match Style",
	"删除":           "Delete",
	"全选":           "Select All",
	"窗口":           "Window",
	"最小化":          "Minimize",
	"缩放":           "Zoom",
	"关闭":           "Close",

	"需要登录":              "Sign in required",
	"登录 Tailscale…":     "Sign in to Tailscale…",
	"连接失败，查看日志":         "Connection failed; view logs",
	"重试连接":              "Retry connection",
	"未连接":               "Disconnected",
	"连接":                "Connect",
	"全局代理 · 已连接":        "Global proxy · Connected",
	"断开连接":              "Disconnect",
	"TUN 已连接":           "TUN connected",
	"代理已连接":             "Proxy connected",
	"出口不可用 · 已阻断":       "Exit unavailable · Traffic blocked",
	"正在读取出口":            "Loading exit nodes",
	"取消":                "Cancel",
	"等待 Tailscale 登录":   "Waiting for Tailscale sign-in",
	"取消连接":              "Cancel connection",
	"正在登录":              "Signing in",
	"取消登录":              "Cancel sign-in",
	"正在断开":              "Disconnecting",
	"正在断开…":             "Disconnecting…",
	"等待安装后台服务":          "Waiting for network service installation",
	"等待出口上线":            "Waiting for exit node",
	"请重新选择出口":           "Select another exit node",
	"正在退出登录":            "Signing out",
	"请稍候…":              "Please wait…",
	"正在连接":              "Connecting",
	"打开":                "Open",
	"打开 TailSplit":      "Open TailSplit",
	"打开 TailSplit 配置面板": "Open TailSplit settings",
	"无法启动本地配置页面，请使用“设置与工具 → 编辑配置…”": "Could not start the settings page. Use Settings & Tools → Edit Configuration…",
	"TailSplit · 未连接":    "TailSplit · Disconnected",
	"连接或断开 TailSplit":    "Connect or disconnect TailSplit",
	"模式：规则分流":            "Mode: Rule-based routing",
	"选择分流策略；已连接时会重新连接":   "Choose routing mode; reconnects when connected",
	"规则分流":               "Rule-based routing",
	"按规则列表和自定义规则决定直连或出口": "Route directly or through an exit using rule lists and custom rules",
	"全局代理（Kill Switch）":  "Global proxy (Kill Switch)",
	"启用 TUN，忽略直连例外；出口断开后阻断，手动断开后恢复系统网络": "Enable TUN and ignore direct exceptions; block traffic if the exit drops, restore networking on manual disconnect",
	"出口节点": "Exit nodes",
	"自动读取当前 Tailnet 的可用出口；选择后重新连接": "Load available Tailnet exits; reconnect after selection",
	"刷新列表": "Refresh list",
	"不创建 TUN、不接管流量、不打开登录浏览器": "Read devices without creating a TUN, routing traffic or opening sign-in",
	"账号与设备":                        "Account & Devices",
	"设备名称":                         "Device name",
	"跟随本机 hostname":                "Use this computer’s hostname",
	"自动使用 tailsplit-<本机 hostname>": "Use tailsplit-<hostname> automatically",
	"自定义名称…":                       "Custom name…",
	"已有登录会保留；正在运行的代理会重新连接": "Keep device sign-in; reconnect the running proxy",
	"Tailnet：正在读取":           "Tailnet: Loading",
	"授权页面：未打开":               "Authorization page: Not opened",
	"打开当前设备的登录页面":            "Open this device’s sign-in page",
	"复制登录链接":                 "Copy sign-in link",
	"复制当前有效的设备授权链接":          "Copy the current device authorization link",
	"退出并切换 Tailnet…":         "Sign Out & Switch Tailnet…",
	"退出此 App 的设备登录，然后重新授权":   "Sign this app’s device out, then authorize again",
	"在配置面板中确认切换网络；未保存的更改会保留": "Confirm the network change in Settings; unsaved edits are kept",
	"启动设置":                   "Startup",
	"登录 macOS 时打开":           "Open at macOS login",
	"通过系统登录项启动 .app":         "Launch the app through system login items",
	"打开 App 后自动连接":           "Connect when app opens",
	"只影响下次打开；需要登录时仍由你主动点击登录": "Applies at next launch; sign-in still requires your action",
	"系统登录项设置…":               "System Login Items…",
	"设置与工具":                  "Settings & Tools",
	"重新连接":                   "Reconnect",
	"停止后使用当前配置重新连接":          "Stop and reconnect using the current configuration",
	"直连 DNS":                 "Direct DNS",
	"跟随当前网络":                 "Use current network",
	"使用物理网卡的系统 DNS；代理域名仍使用出口侧 DNS": "Use the physical adapter’s DNS; proxied domains still use exit-side DNS",
	"当前上游":      "Current upstream",
	"编辑固定 DNS…": "Edit Fixed DNS…",
	"在配置中设置 dns.direct；保存后重新连接": "Set dns.direct in the configuration; reconnect after saving",
	"编辑配置…":         "Edit Configuration…",
	"显示配置位置":        "Show Configuration Location",
	"复制本机 SOCKS 地址": "Copy Local SOCKS Address",
	"查看日志…":         "View Logs…",
	"详细日志（Debug）":   "Detailed Logging (Debug)",
	"记录 DNS、分流规则和连接诊断；切换后自动重连，关闭后恢复普通日志": "Log DNS, routing and connection diagnostics; reconnect on change, restore normal logging when disabled",
	"后台网络服务":  "Background Network Service",
	"更新或修复…":  "Update or Repair…",
	"打开系统安装器": "Open the system installer",
	"卸载后台服务…": "Uninstall Network Service…",
	"停止代理并打开卸载包；保留登录和配置": "Stop the proxy and open the uninstaller; keep sign-in and configuration",
	"检查更新…": "Check for Updates…",
	"检查 GitHub 正式版；安装前会确认":  "Check stable GitHub releases; ask before installing",
	"自动检查更新":                "Automatically Check for Updates",
	"启动后和每六小时检查；安装始终由你确认":   "Check after launch and every six hours; always ask before installing",
	"关于 TailSplit…":         "About TailSplit…",
	"查看当前 App 的版本号和构建号":     "View the app version and build",
	"退出 TailSplit":          "Quit TailSplit",
	"同时停止此 App 启动的代理":       "Stop the proxy started by this app and quit",
	"登录时打开（等待系统允许）":         "Open at Login (Awaiting Approval)",
	"登录页面：复制失败":             "Sign-in page: Copy failed",
	"登录页面：链接已复制":            "Sign-in page: Link copied",
	"设置与工具 · 有更新":           "Settings & Tools · Update Available",
	"授权页面：":                 "Authorization page: ",
	"设备名称：":                 "Device name: ",
	"模式：全局代理 · Kill Switch": "Mode: Global proxy · Kill Switch",
	"直连 DNS：跟随系统":           "Direct DNS: System",
	"物理网卡的原始 DNS":           "Original physical adapter DNS",
	"直连 DNS：":               "Direct DNS: ",
	"固定：":                   "Fixed: ",
	"自定义：":                  "Custom: ",
	"Tailnet：":              "Tailnet: ",
	"Tailnet：等待连接":          "Tailnet: Waiting for connection",
	"出口：":                   "Exit: ",
	"选择出口节点":                "Select Exit Node",
	"登录后刷新出口列表":             "Sign in, then refresh exit nodes",
	"（离线或已过期）":              " (Offline or expired)",
	"选择后保存配置；正在运行的代理会重新连接": "Save the selected exit; reconnect the running proxy",
	"等待确认更新…":         "Waiting for update confirmation…",
	"等待确认升级…":         "Waiting for update confirmation…",
	"正在下载更新…":         "Downloading Update…",
	"正在准备升级…":         "Preparing Update…",
	"正在检查更新…":         "Checking for Updates…",
	"升级到 ":            "Update to ",
	"检查更新失败，重试…":      "Update Check Failed; Retry…",
	"已是最新版本 · 检查更新…":  "Up to Date · Check for Updates…",
	"请先检查并选择可用的新版本":   "Check for updates and select an available version first",
	"可用版本已变化，请重新确认升级": "The available version changed; confirm the update again",
	"正在准备新授权链接":       "Preparing a new authorization link",
	"未打开":             "Not opened",
	"连接失败，请查看日志":      "Connection failed; view logs",
	"当前配置无需设备登录":      "This configuration does not require device sign-in",
	"设备已登录":           "Device signed in",
	"正在打开默认浏览器":       "Opening the default browser",
	"打开失败，请复制登录链接":    "Could not open browser; copy the sign-in link",
	"已请求浏览器打开":        "Browser open requested",
	"无法准备登录，请查看日志":    "Could not prepare sign-in; view logs",
	"无法重新登录，请查看日志":    "Could not sign in again; view logs",

	"配置文件已更新，请重新读取后重试。": "The configuration changed. Reload it and try again.",

	"App 更新失败，已保留原版本。请重新检查更新，或从 GitHub Release 安装。": "App update failed. The previous version was kept. Check again or install from GitHub Releases.",
	"无法准备配置文件": "Could not prepare the configuration file",
	"无法打开日志文件": "Could not open the log file",
	"设备名称须为 1–63 位英文字母、数字或连字符，或留空跟随本机 hostname": "Use 1–63 English letters, digits or hyphens for the device name, or leave blank to use this computer’s hostname",
	"无法刷新出口列表，请检查网络和后台服务":                       "Could not refresh exit nodes; check the network and background service",
	"无法应用设置，请检查配置和后台服务":                         "Could not apply settings; check the configuration and background service",
	"无法更新登录项，请从打包的 TailSplit.app 操作或检查系统登录项设置":  "Could not update the login item; use the packaged TailSplit.app or check System Login Items",
	"无法保存自动连接设置，请检查配置文件":                        "Could not save automatic connection settings; check the configuration file",
	"连接、账号、分流与日志":                               "Connection, account, routing and logs",
	"使用当前配置连接":                                  "Connect using the current configuration",
	"断开":                                        "Disconnect",
	"停止网络会话并恢复受管理的网络":                           "Stop the session and restore managed networking",
	"登录 Tailnet…":                               "Sign in to Tailnet…",
	"在默认浏览器中授权本机设备":                             "Authorize this device in the default browser",
	"退出设备登录":                                    "Sign Out Device",
	"先断开，再退出当前设备登录":                             "Disconnect and sign out this device",
	"刷新出口节点":                                    "Refresh Exit Nodes",
	"只读取设备，不创建 TUN":                             "Read devices without creating a TUN",
	"登录 Windows 后启动":                            "Open at Windows Login",
	"只启动界面；自动连接另行设置":                            "Open the interface; automatic connection is configured separately",
	"启动后和每六小时检查":                                "Check after launch and every six hours",
	"关于 TailSplit":                              "About TailSplit",
	"版本与源代码信息":                                  "Version and source information",
	"先断开网络，再退出":                                 "Disconnect before quitting",
	"源代码：":                                      "Source: ",
	"升级到 TailSplit %s？\n\n将下载并校验安装包，然后打开安装向导。安装时会断开连接；配置和设备登录会保留。Windows 将请求管理员批准。": "Update to TailSplit %s?\n\nDownload and verify the installer, then open the setup wizard. Installation disconnects the session; configuration and device sign-in are kept. Windows will request administrator approval.",
	"TailSplit 更新": "TailSplit Update",
}
