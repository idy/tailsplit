//go:build darwin

package main

import (
	"bytes"
	"context"
	"flag"
	"image"
	"image/color"
	"image/png"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/configweb"
	"github.com/idy/tailsplit/internal/tray"

	"github.com/getlantern/systray"
	"gopkg.in/yaml.v3"
)

type configurationApply struct {
	ctx    context.Context
	update func() error
	done   chan error
}

func main() {
	executable, err := os.Executable()
	if err != nil {
		return
	}
	directory := filepath.Dir(executable)
	configRoot, _ := os.UserConfigDir()
	defaultConfig := filepath.Join(configRoot, "TailSplit", "config.yaml")
	defaultTemplate := filepath.Join(directory, "..", "Resources", "config.example.yaml")
	if _, err := os.Stat(defaultTemplate); os.IsNotExist(err) {
		defaultTemplate = filepath.Join(directory, "..", "config.example.yaml")
	}
	config := flag.String("config", defaultConfig, "configuration path")
	helper := flag.String("helper", filepath.Join(directory, "tailsplit"), "bundled helper")
	template := flag.String("template", defaultTemplate, "initial configuration template")
	logPath := flag.String("log", filepath.Join(configRoot, "TailSplit", "logs", "tailsplit.log"), "private log file")
	startImmediately := flag.Bool("start", false, "start the proxy explicitly after opening the app")
	loginImmediately := flag.Bool("login", false, "start and open the current device login page when ready")
	flag.Parse()
	if err := initializeConfig(*config, *template); err != nil {
		showError("无法准备配置文件")
		return
	}
	preferences, _ := tray.ReadPreferences(*config)
	controller, err := tray.New(tray.Options{Helper: *helper, ConfigPath: *config, LogPath: *logPath})
	if err != nil {
		showError("无法打开日志文件")
		return
	}
	configurationRequests := make(chan configurationApply)
	configurationPage, configurationErr := configweb.Start(configweb.Options{
		ConfigPath: *config, Snapshot: controller.Snapshot,
		Apply: func(ctx context.Context, update func() error) error {
			request := configurationApply{ctx: ctx, update: update, done: make(chan error, 1)}
			select {
			case configurationRequests <- request:
			case <-ctx.Done():
				return ctx.Err()
			}
			select {
			case err := <-request.done:
				return err
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})
	systray.Run(func() {
		icon := trayIcon()
		systray.SetTemplateIcon(icon, icon)
		systray.SetTooltip("TailSplit")
		status := systray.AddMenuItem("TailSplit · 未连接", "")
		status.Disable()
		connect := systray.AddMenuItem("连接", "连接或断开 TailSplit")
		routing := systray.AddMenuItem("模式：规则分流", "选择分流策略；已连接时会重新连接")
		splitMode := routing.AddSubMenuItemCheckbox("规则分流", "按 GFWList 和自定义规则决定直连或出口", preferences.RoutingMode != tailsplit.RoutingGlobal)
		globalMode := routing.AddSubMenuItemCheckbox("全局代理（Kill Switch）", "启用 TUN，忽略直连例外；出口断开后阻断，手动断开后恢复系统网络", preferences.RoutingMode == tailsplit.RoutingGlobal)
		exitMenu := systray.AddMenuItem("出口节点", "自动读取当前 Tailnet 的可用出口；选择后重新连接")
		picker := newExitPicker(exitMenu)
		refreshExits := exitMenu.AddSubMenuItem("刷新列表", "不创建 TUN、不接管流量、不打开登录浏览器")
		systray.AddSeparator()
		account := systray.AddMenuItem("账号与设备", "")
		device := account.AddSubMenuItem("设备名称", "")
		autoName := device.AddSubMenuItemCheckbox("跟随本机 hostname", "自动使用 tailsplit-<本机 hostname>", preferences.DeviceName == "")
		customName := device.AddSubMenuItem("自定义名称…", "已有登录会保留；正在运行的代理会重新连接")
		network := account.AddSubMenuItem("Tailnet：正在读取", "")
		network.Disable()
		loginStatus := account.AddSubMenuItem("授权页面：未打开", "")
		loginStatus.Disable()
		loginStatus.Hide()
		login := account.AddSubMenuItem("登录 Tailscale…", "打开当前设备的登录页面")
		copyLogin := account.AddSubMenuItem("复制登录链接", "复制当前有效的设备授权链接")
		copyLogin.Hide()
		logout := account.AddSubMenuItem("退出并切换 Tailnet…", "退出此 App 的设备登录，然后重新授权")
		startup := systray.AddMenuItem("启动设置", "")
		startAtLogin := startup.AddSubMenuItemCheckbox("登录 macOS 时打开", "通过系统登录项启动 .app", false)
		connectOnStart := startup.AddSubMenuItemCheckbox("打开 App 后自动连接", "只影响下次打开；需要登录时仍由你主动点击登录", preferences.ConnectOnStart)
		loginItemSettings := startup.AddSubMenuItem("系统登录项设置…", "")
		tools := systray.AddMenuItem("设置与工具", "")
		restart := tools.AddSubMenuItem("重新连接", "停止后使用当前配置重新连接")
		configuration := tools.AddSubMenuItem("配置面板…", "在本机网页中查看、编辑和校验配置")
		if configurationErr != nil {
			configuration.Disable()
			configuration.SetTooltip("无法启动本地配置页面，仍可使用“编辑配置…”")
		}
		dnsMenu := tools.AddSubMenuItem("直连 DNS", "")
		systemDNS := dnsMenu.AddSubMenuItemCheckbox("跟随当前网络", "使用物理网卡的系统 DNS；代理域名仍使用出口侧 DNS", preferences.DirectDNS == "system")
		dnsInfo := dnsMenu.AddSubMenuItem("当前上游", "")
		dnsInfo.Disable()
		editDNS := dnsMenu.AddSubMenuItem("编辑固定 DNS…", "在配置中设置 dns.direct；保存后重新连接")
		edit := tools.AddSubMenuItem("编辑配置…", "")
		reveal := tools.AddSubMenuItem("显示配置位置", "")
		copyAddress := tools.AddSubMenuItem("复制本机 SOCKS 地址", "")
		logs := tools.AddSubMenuItem("查看日志…", "")
		debugLogging := tools.AddSubMenuItemCheckbox("详细日志（Debug）", "记录 DNS、分流规则和连接诊断；切换后自动重连，关闭后恢复普通日志", preferences.DebugLogging)
		service := tools.AddSubMenuItem("后台网络服务", "")
		installService := service.AddSubMenuItem("更新或修复…", "打开系统安装器")
		removeService := service.AddSubMenuItem("卸载后台服务…", "停止代理并打开卸载包；保留登录和配置")
		about := tools.AddSubMenuItem("关于 TailSplit…", "查看当前 App 的版本号和构建号")
		restart.Disable()
		systray.AddSeparator()
		quit := systray.AddMenuItem("退出 TailSplit", "同时停止此 App 启动的代理")
		if *loginImmediately {
			_ = controller.StartLogin()
		} else if *startImmediately || preferences.ConnectOnStart {
			_ = controller.Start()
		} else {
			_ = controller.RefreshExitNodes()
		}
		go func() {
			defer close(picker.done)
			ticker := time.NewTicker(500 * time.Millisecond)
			defer ticker.Stop()
			var previous tray.Snapshot
			var previousLoginMessage string
			var flow tray.LoginFlow
			if *loginImmediately {
				flow.Request()
			}
			browserDone := make(chan error, 1)
			logoutDone := make(chan error, 1)
			loggingOut := false
			editing := false
			settingsDone := make(chan error, 1)
			reconfigure := func(update func() error) {
				editing = true
				flow.Cancel()
				go func() { settingsDone <- controller.Reconfigure(update) }()
			}
			requestLogin := func() {
				snapshot := controller.Snapshot()
				if !snapshot.Active {
					if controller.StartLogin() != nil {
						flow.Fail("无法准备登录，请查看日志")
						return
					}
				}
				flow.Request()
			}
			loginItemStatus := tray.LoginItemUnavailable
			refreshLoginItem := func() {
				loginItemStatus = tray.LoginItem()
				setChecked(startAtLogin, loginItemStatus == tray.LoginItemEnabled ||
					loginItemStatus == tray.LoginItemNeedsApproval)
				setEnabled(startAtLogin, loginItemStatus != tray.LoginItemUnavailable)
				if loginItemStatus == tray.LoginItemNeedsApproval {
					startAtLogin.SetTitle("登录时打开（等待系统允许）")
				} else {
					startAtLogin.SetTitle("登录 macOS 时打开")
				}
			}
			refreshLoginItem()
			ticks := 0
			openReadyLogin := func(snapshot tray.Snapshot) {
				if loggingOut {
					return
				}
				if value := flow.Next(snapshot); value != "" {
					login.Disable()
					go func() {
						ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
						defer cancel()
						browserDone <- tray.OpenLoginPage(ctx, value)
					}()
				}
			}
			for {
				select {
				case <-connect.ClickedCh:
					if editing || loggingOut || flow.Opening() {
						continue
					}
					snapshot := controller.Snapshot()
					if !snapshot.Active && snapshot.BackendState == "NeedsLogin" {
						requestLogin()
						openReadyLogin(controller.Snapshot())
					} else if snapshot.Active {
						flow.Cancel()
						editing = true
						go func() { settingsDone <- controller.Stop() }()
					} else {
						_ = controller.Start()
					}
				case <-restart.ClickedCh:
					if editing || loggingOut || flow.Opening() || !canReconnect(controller.Snapshot()) {
						continue
					}
					flow.Cancel()
					editing = true
					go func() { settingsDone <- controller.Restart() }()
				case <-splitMode.ClickedCh:
					previous = tray.Snapshot{} // Repaint even when the selected mode is clicked again.
					if !editing && !loggingOut && !flow.Opening() && preferences.RoutingMode != tailsplit.RoutingSplit {
						reconfigure(func() error { return tray.SetRoutingMode(*config, tailsplit.RoutingSplit) })
					}
				case <-globalMode.ClickedCh:
					previous = tray.Snapshot{}
					if !editing && !loggingOut && !flow.Opening() && preferences.RoutingMode != tailsplit.RoutingGlobal {
						reconfigure(func() error { return tray.SetRoutingMode(*config, tailsplit.RoutingGlobal) })
					}
				case <-login.ClickedCh:
					if loggingOut || editing || flow.Opening() {
						continue
					}
					requestLogin()
					openReadyLogin(controller.Snapshot())
				case err := <-browserDone:
					controller.RecordLoginBrowserResult(err)
					flow.Complete(err)
				case <-copyLogin.ClickedCh:
					if value := controller.Snapshot().LoginURL; value != "" {
						command := exec.Command("/usr/bin/pbcopy")
						command.Stdin = strings.NewReader(value)
						if err := command.Run(); err != nil {
							loginStatus.SetTitle("登录页面：复制失败")
						} else {
							loginStatus.SetTitle("登录页面：链接已复制")
						}
					}
				case <-logout.ClickedCh:
					if loggingOut || editing || flow.Opening() {
						continue
					}
					flow.Cancel()
					loggingOut = true
					login.Disable()
					go func() {
						err := controller.Logout()
						if err == nil {
							err = controller.StartLogin()
						}
						logoutDone <- err
					}()
				case err := <-logoutDone:
					loggingOut = false
					if err == nil {
						flow.Request()
						openReadyLogin(controller.Snapshot())
					} else {
						flow.Fail("无法重新登录，请查看日志")
					}
				case <-customName.ClickedCh:
					if editing || loggingOut || flow.Opening() {
						continue
					}
					value, saved := tray.EditDeviceName(preferences.DeviceName)
					if !saved {
						continue
					}
					if _, err := tray.DeviceName(value); err != nil {
						showError("设备名称须为 1–63 位英文字母、数字或连字符，或留空跟随本机 hostname")
						continue
					}
					reconfigure(func() error { return tray.SetDeviceName(*config, value) })
				case <-autoName.ClickedCh:
					previous = tray.Snapshot{}
					if !editing && !loggingOut && !flow.Opening() && preferences.DeviceName != "" {
						reconfigure(func() error { return tray.SetDeviceName(*config, "") })
					}
				case <-systemDNS.ClickedCh:
					previous = tray.Snapshot{}
					if !editing && !loggingOut && !flow.Opening() && preferences.DirectDNS != "system" {
						reconfigure(func() error { return tray.SetSystemDNS(*config) })
					}
				case <-editDNS.ClickedCh:
					openPath("-t", *config)
				case id := <-picker.clicks:
					if editing || loggingOut || flow.Opening() {
						continue
					}
					for _, node := range controller.Snapshot().ExitNodes {
						if node.ID == id && node.Online {
							reconfigure(func() error { return tray.SetExitNode(*config, id) })
							break
						}
					}
				case <-refreshExits.ClickedCh:
					if !editing && !loggingOut {
						if err := controller.RefreshExitNodes(); err != nil {
							showError("无法刷新出口列表，请检查网络和后台服务")
						}
					}
				case request := <-configurationRequests:
					if editing || loggingOut || flow.Opening() || controller.Snapshot().State == "authorizing" {
						request.done <- configweb.ErrBusy
						continue
					}
					if err := request.ctx.Err(); err != nil {
						request.done <- err
						continue
					}
					editing = true
					flow.Cancel()
					go func() {
						err := controller.Reconfigure(request.update)
						// The browser reports errors without a second native alert.
						settingsDone <- nil
						request.done <- err
					}()
				case err := <-settingsDone:
					editing = false
					if updated, readErr := tray.ReadPreferences(*config); readErr == nil {
						preferences = updated
					}
					previous = tray.Snapshot{}
					if err != nil {
						showError("无法应用设置，请检查配置和后台服务")
					}
				case <-startAtLogin.ClickedCh:
					enable := loginItemStatus != tray.LoginItemEnabled && loginItemStatus != tray.LoginItemNeedsApproval
					if err := tray.SetLoginItem(enable); err != nil {
						showError("无法更新登录项，请从打包的 TailSplit.app 操作或检查系统登录项设置")
					}
					refreshLoginItem()
					if enable && loginItemStatus == tray.LoginItemNeedsApproval {
						tray.OpenLoginItemSettings()
					}
				case <-connectOnStart.ClickedCh:
					if editing {
						continue
					}
					value := !preferences.ConnectOnStart
					if err := tray.SetConnectOnStart(*config, value); err != nil {
						showError("无法保存自动连接设置，请检查配置文件")
					} else {
						preferences.ConnectOnStart = value
						setChecked(connectOnStart, value)
					}
				case <-loginItemSettings.ClickedCh:
					tray.OpenLoginItemSettings()
				case <-configuration.ClickedCh:
					if configurationPage != nil {
						openPath(configurationPage.URL())
					}
				case <-edit.ClickedCh:
					openPath("-t", *config)
				case <-reveal.ClickedCh:
					openPath("-R", *config)
				case <-logs.ClickedCh:
					openPath("-t", *logPath)
				case <-debugLogging.ClickedCh:
					if !editing && !loggingOut && !flow.Opening() {
						enabled := !preferences.DebugLogging
						reconfigure(func() error { return tray.SetDebugLogging(*config, enabled) })
					}
				case <-about.ClickedCh:
					tray.ShowAbout()
				case <-installService.ClickedCh:
					go func() {
						if controller.Stop() == nil {
							openPath("-a", "Installer", filepath.Join(directory, "..", "Resources", "TailSplit Network Service.pkg"))
						}
					}()
				case <-removeService.ClickedCh:
					go func() {
						if controller.Stop() == nil {
							openPath("-a", "Installer", filepath.Join(directory, "..", "Resources", "Remove TailSplit Network Service.pkg"))
						}
					}()
				case <-copyAddress.ClickedCh:
					if value := proxyAddress(*config); value != "" {
						command := exec.Command("/usr/bin/pbcopy")
						command.Stdin = strings.NewReader("socks5h://" + value)
						_ = command.Run()
					}
				case <-quit.ClickedCh:
					_ = controller.Stop()
					systray.Quit()
					return
				case <-ticker.C:
					ticks++
					if ticks%10 == 0 {
						refreshLoginItem()
					}
					snapshot := controller.Snapshot()
					openReadyLogin(snapshot)
					loginMessage := flow.Message()
					if reflect.DeepEqual(snapshot, previous) && loginMessage == previousLoginMessage && !editing {
						continue
					}
					previous = snapshot
					previousLoginMessage = loginMessage
					loginStatus.SetTitle("授权页面：" + loginMessage)
					if loginMessage != "未打开" && loginMessage != "设备已登录" {
						loginStatus.Show()
					} else {
						loginStatus.Hide()
					}
					presentation := connectionPresentation(snapshot)
					status.SetTitle("TailSplit · " + presentation.Summary)
					connect.SetTitle(presentation.Action)
					systray.SetTooltip("TailSplit · " + presentation.Summary)
					name, _ := tray.DeviceName(preferences.DeviceName)
					device.SetTitle("设备名称：" + shortMenuTitle(name))
					setChecked(autoName, preferences.DeviceName == "")
					setChecked(splitMode, preferences.RoutingMode != tailsplit.RoutingGlobal)
					setChecked(globalMode, preferences.RoutingMode == tailsplit.RoutingGlobal)
					if preferences.RoutingMode == tailsplit.RoutingGlobal {
						routing.SetTitle("模式：全局代理 · Kill Switch")
					} else {
						routing.SetTitle("模式：规则分流")
					}
					setChecked(systemDNS, preferences.DirectDNS == "system")
					setChecked(debugLogging, preferences.DebugLogging)
					if preferences.DirectDNS == "system" {
						dnsMenu.SetTitle("直连 DNS：跟随系统")
						dnsInfo.SetTitle("物理网卡的原始 DNS")
					} else {
						dnsMenu.SetTitle("直连 DNS：" + preferences.DirectDNS)
						dnsInfo.SetTitle("固定：" + preferences.DirectDNS)
					}
					autoHostname, _ := tray.DeviceName("")
					autoName.SetTooltip(autoHostname)
					if preferences.DeviceName != "" {
						customName.SetTitle("自定义：" + shortMenuTitle(preferences.DeviceName) + "…")
					} else {
						customName.SetTitle("自定义名称…")
					}
					if snapshot.NetworkName != "" {
						network.SetTitle("Tailnet：" + snapshot.NetworkName)
					} else {
						network.SetTitle("Tailnet：等待连接")
					}
					if snapshot.ExitNode != "" {
						exitMenu.SetTitle("出口：" + shortMenuTitle(snapshot.ExitNode))
					} else {
						exitMenu.SetTitle("选择出口节点")
					}
					busy := editing || loggingOut || snapshot.State == "stopping" || snapshot.State == "logging-out"
					setEnabled(connect, !busy && !flow.Opening())
					setEnabled(restart, canReconnect(snapshot) && !busy && !flow.Opening())
					setEnabled(logout, !busy && !flow.Opening())
					setEnabled(login, !busy && !flow.Opening() &&
						snapshot.Mode != "discovery" &&
						snapshot.BackendState != "Running" && snapshot.BackendState != "Direct")
					if snapshot.LoginURL != "" {
						copyLogin.Show()
					} else {
						copyLogin.Hide()
					}
					setEnabled(copyLogin, !busy)
					setEnabled(device, !busy && !flow.Opening())
					setEnabled(routing, !busy && !flow.Opening())
					setEnabled(connectOnStart, !editing)
					setEnabled(systemDNS, !busy && !flow.Opening())
					setEnabled(debugLogging, !busy && !flow.Opening())
					setEnabled(refreshExits, !busy && !flow.Opening())
					setEnabled(installService, !busy && snapshot.State != "authorizing")
					setEnabled(removeService, !busy && snapshot.State != "authorizing")
					picker.update(snapshot, preferences.ExitNode, !busy && !flow.Opening())
				}
			}
		}()
	}, func() {
		if configurationPage != nil {
			_ = configurationPage.Close()
		}
		_ = controller.Close()
	})
}

func openPath(arguments ...string) {
	go func() { _ = exec.Command("/usr/bin/open", arguments...).Run() }()
}

func setEnabled(item *systray.MenuItem, enabled bool) {
	if enabled {
		item.Enable()
	} else {
		item.Disable()
	}
}

func initializeConfig(path, template string) error {
	if _, err := os.Stat(path); err == nil {
		// This was the shipped name of every 0.2.x installation, not a
		// machine-specific choice. Preserve every other explicit name.
		if info, statErr := os.Lstat(path); statErr == nil && info.Mode().IsRegular() {
			if preferences, readErr := tray.ReadPreferences(path); readErr == nil &&
				preferences.DeviceName == "letsolo-tailsplit" {
				return tray.SetDeviceName(path, "")
			}
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	data, err := os.ReadFile(template)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = file.Write(data)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func proxyAddress(path string) string {
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 1<<20 {
		return ""
	}
	var config struct {
		Listen string `yaml:"listen"`
	}
	if yaml.Unmarshal(data, &config) != nil {
		return ""
	}
	if config.Listen == "" {
		config.Listen = "127.0.0.1:17890"
	}
	value, err := netip.ParseAddrPort(config.Listen)
	if err != nil || !value.Addr().IsLoopback() || value.Port() == 0 {
		return ""
	}
	return value.String()
}

func showError(message string) {
	// Message is fixed application text, never a key, path or log value.
	_ = exec.Command("/usr/bin/osascript", "-e",
		`display alert "TailSplit" message "`+message+`" as critical`).Run()
}

func trayIcon() []byte {
	canvas := image.NewNRGBA(image.Rect(0, 0, 24, 24))
	line := func(x0, y0, x1, y1 int) {
		steps := max(abs(x1-x0), abs(y1-y0))
		for step := 0; step <= steps; step++ {
			x, y := x0+(x1-x0)*step/steps, y0+(y1-y0)*step/steps
			for dx := 0; dx < 2; dx++ {
				for dy := 0; dy < 2; dy++ {
					canvas.SetNRGBA(x+dx, y+dy, color.NRGBA{A: 255})
				}
			}
		}
	}
	line(11, 20, 11, 13)
	line(11, 13, 4, 6)
	line(11, 13, 18, 6)
	line(4, 6, 4, 11)
	line(4, 6, 9, 6)
	line(18, 6, 13, 6)
	line(18, 6, 18, 11)
	var buffer bytes.Buffer
	_ = png.Encode(&buffer, canvas)
	return buffer.Bytes()
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
