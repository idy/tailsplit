//go:build darwin

package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"image/png"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/appupdate"
	"github.com/idy/tailsplit/internal/configweb"
	"github.com/idy/tailsplit/internal/locale"
	"github.com/idy/tailsplit/internal/tray"

	"github.com/getlantern/systray"
	"gopkg.in/yaml.v3"
)

type configurationApply struct {
	ctx    context.Context
	update func() error
	direct bool
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
	noConnect := flag.Bool("no-connect", false, "start disconnected after an update")
	updateFailed := flag.Bool("update-failed", false, "report a failed update and retained app")
	flag.Parse()
	uiLanguage = locale.NewStore(*config)
	tray.SetUILanguage(currentLanguage())
	if *updateFailed {
		showError(tr("App 更新失败，已保留原版本。请重新检查更新，或从 GitHub Release 安装。"))
	}
	if err := initializeConfig(*config, *template); err != nil {
		showError(tr("无法准备配置文件"))
		return
	}
	preferences, err := tray.ReadPreferences(*config)
	if err != nil {
		// Keep the documented default; the panel reports the configuration error.
		preferences.CheckForUpdates = true
	}
	controller, err := tray.New(tray.Options{Helper: *helper, ConfigPath: *config, LogPath: *logPath})
	if err != nil {
		showError(tr("无法打开日志文件"))
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	configurationRequests := make(chan configurationApply)
	apply := func(requestCtx context.Context, update func() error, direct bool) error {
		request := configurationApply{ctx: requestCtx, update: update, direct: direct, done: make(chan error, 1)}
		select {
		case configurationRequests <- request:
		case <-requestCtx.Done():
			return requestCtx.Err()
		case <-ctx.Done():
			return ctx.Err()
		}
		select {
		case err := <-request.done:
			return err
		case <-requestCtx.Done():
			return requestCtx.Err()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	updates := newDesktopUpdates(ctx, tray.AppVersion(), preferences.CheckForUpdates,
		func(ctx context.Context, enabled bool) error {
			return apply(ctx, func() error { return tray.SetCheckForUpdates(*config, enabled) }, true)
		},
		func(update appupdate.Package) error {
			return installDarwinUpdate(ctx, update, executable, *config, *logPath, func(commit func() error) error {
				// A settings save, login or logout is brief: wait for it rather than
				// abort a confirmed update. The worker waits 90 seconds for this exit.
				deadline := time.Now().Add(60 * time.Second)
				for {
					err := apply(ctx, func() error {
						if err := controller.Close(); err != nil {
							return err
						}
						return commit()
					}, true)
					if errors.Is(err, configweb.ErrBusy) && time.Now().Before(deadline) {
						select {
						case <-ctx.Done():
							return ctx.Err()
						case <-time.After(500 * time.Millisecond):
						}
						continue
					}
					// Finish the successful handoff before Quit cancels the app
					// context; cancellation must not delete the worker's package.
					if err == nil {
						systray.Quit()
					}
					return err
				}
			})
		})
	configurationPage, configurationErr := configweb.Start(configweb.Options{
		Language: uiLanguage, ConfigPath: *config, Snapshot: controller.Snapshot,
		Apply:       func(ctx context.Context, update func() error) error { return apply(ctx, update, false) },
		UpdateState: updates.snapshot, UpdateAction: updates.action,
	})
	systray.Run(func() {
		setTrayIcon(false)
		systray.SetTooltip("TailSplit")
		configuration := addMenu(nil, "打开", "打开 TailSplit 配置面板")
		if configurationErr != nil {
			configuration.Disable()
			configuration.SetTooltip(tr("无法启动本地配置页面，请使用“设置与工具 → 编辑配置…”"))
		}
		systray.AddSeparator()
		status := addMenu(nil, "TailSplit · 未连接", "")
		status.Disable()
		connect := addMenu(nil, "连接", "连接或断开 TailSplit")
		routing := addMenu(nil, "模式：规则分流", "选择分流策略；已连接时会重新连接")
		splitMode := addMenu(routing, "规则分流", "按 GFWList 和自定义规则决定直连或出口", preferences.RoutingMode != tailsplit.RoutingGlobal)
		globalMode := addMenu(routing, "全局代理（Kill Switch）", "启用 TUN，忽略直连例外；出口断开后阻断，手动断开后恢复系统网络", preferences.RoutingMode == tailsplit.RoutingGlobal)
		exitMenu := addMenu(nil, "出口节点", "自动读取当前 Tailnet 的可用出口；选择后重新连接")
		picker := newExitPicker(exitMenu)
		refreshExits := addMenu(exitMenu, "刷新列表", "不创建 TUN、不接管流量、不打开登录浏览器")
		systray.AddSeparator()
		account := addMenu(nil, "账号与设备", "")
		device := addMenu(account, "设备名称", "")
		autoName := addMenu(device, "跟随本机 hostname", "自动使用 tailsplit-<本机 hostname>", preferences.DeviceName == "")
		customName := addMenu(device, "自定义名称…", "已有登录会保留；正在运行的代理会重新连接")
		network := addMenu(account, "Tailnet：正在读取", "")
		network.Disable()
		loginStatus := addMenu(account, "授权页面：未打开", "")
		loginStatus.Disable()
		loginStatus.Hide()
		login := addMenu(account, "登录 Tailscale…", "打开当前设备的登录页面")
		copyLogin := addMenu(account, "复制登录链接", "复制当前有效的设备授权链接")
		copyLogin.Hide()
		logout := addMenu(account, "退出并切换 Tailnet…", "退出此 App 的设备登录，然后重新授权")
		startup := addMenu(nil, "启动设置", "")
		startAtLogin := addMenu(startup, "登录 macOS 时打开", "通过系统登录项启动 .app", false)
		connectOnStart := addMenu(startup, "打开 App 后自动连接", "只影响下次打开；需要登录时仍由你主动点击登录", preferences.ConnectOnStart)
		loginItemSettings := addMenu(startup, "系统登录项设置…", "")
		tools := addMenu(nil, "设置与工具", "")
		restart := addMenu(tools, "重新连接", "停止后使用当前配置重新连接")
		dnsMenu := addMenu(tools, "直连 DNS", "")
		systemDNS := addMenu(dnsMenu, "跟随当前网络", "使用物理网卡的系统 DNS；代理域名仍使用出口侧 DNS", preferences.DirectDNS == "system")
		dnsInfo := addMenu(dnsMenu, "当前上游", "")
		dnsInfo.Disable()
		editDNS := addMenu(dnsMenu, "编辑固定 DNS…", "在配置中设置 dns.direct；保存后重新连接")
		edit := addMenu(tools, "编辑配置…", "")
		reveal := addMenu(tools, "显示配置位置", "")
		copyAddress := addMenu(tools, "复制本机 SOCKS 地址", "")
		logs := addMenu(tools, "查看日志…", "")
		debugLogging := addMenu(tools, "详细日志（Debug）", "记录 DNS、分流规则和连接诊断；切换后自动重连，关闭后恢复普通日志", preferences.DebugLogging)
		service := addMenu(tools, "后台网络服务", "")
		installService := addMenu(service, "更新或修复…", "打开系统安装器")
		removeService := addMenu(service, "卸载后台服务…", "停止代理并打开卸载包；保留登录和配置")
		checkUpdate := addMenu(tools, "检查更新…", "检查 GitHub 正式版；安装前会确认")
		autoUpdates := addMenu(tools, "自动检查更新", "启动后和每六小时检查；安装始终由你确认", preferences.CheckForUpdates)
		about := addMenu(tools, "关于 TailSplit…", "查看当前 App 的版本号和构建号")
		restart.Disable()
		systray.AddSeparator()
		quit := addMenu(nil, "退出 TailSplit", "同时停止此 App 启动的代理")
		if *loginImmediately {
			_ = controller.StartLogin()
		} else if !*noConnect && (*startImmediately || preferences.ConnectOnStart) {
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
			var connected bool
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
					startAtLogin.SetTitle(tr("登录时打开（等待系统允许）"))
				} else {
					startAtLogin.SetTitle(tr("登录 macOS 时打开"))
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
							loginStatus.SetTitle(tr("登录页面：复制失败"))
						} else {
							loginStatus.SetTitle(tr("登录页面：链接已复制"))
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
						showError(tr("设备名称须为 1–63 位英文字母、数字或连字符，或留空跟随本机 hostname"))
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
							showError(tr("无法刷新出口列表，请检查网络和后台服务"))
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
						var err error
						if request.direct {
							err = request.update()
						} else {
							err = controller.Reconfigure(request.update)
						}
						// The browser reports errors without a second native alert.
						settingsDone <- nil
						request.done <- err
					}()
				case err := <-settingsDone:
					editing = false
					if updated, readErr := tray.ReadPreferences(*config); readErr == nil {
						preferences = updated
						updates.updater.SetAutomatic(updated.CheckForUpdates)
					}
					previous = tray.Snapshot{}
					if err != nil {
						showError(tr("无法应用设置，请检查配置和后台服务"))
					}
				case <-startAtLogin.ClickedCh:
					enable := loginItemStatus != tray.LoginItemEnabled && loginItemStatus != tray.LoginItemNeedsApproval
					if err := tray.SetLoginItem(enable); err != nil {
						showError(tr("无法更新登录项，请从打包的 TailSplit.app 操作或检查系统登录项设置"))
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
						showError(tr("无法保存自动连接设置，请检查配置文件"))
					} else {
						preferences.ConnectOnStart = value
						setChecked(connectOnStart, value)
					}
				case <-loginItemSettings.ClickedCh:
					tray.OpenLoginItemSettings()
				case <-configuration.ClickedCh:
					if configurationPage != nil {
						if err := tray.ShowConfigurationWindow(configurationPage.URL()); err != nil {
							controller.RecordDesktopEvent("Configuration window unavailable; opening the panel in the default browser: " + err.Error())
							openPath(configurationPage.URL())
						}
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
				case <-checkUpdate.ClickedCh:
					action := "check"
					if updates.snapshot().Available {
						action = "install"
					}
					go func() { _ = updates.action(ctx, action) }()
				case <-autoUpdates.ClickedCh:
					action := "automatic-on"
					if updates.snapshot().Automatic {
						action = "automatic-off"
					}
					go func() { _ = updates.action(ctx, action) }()
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
					language := currentLanguage()
					languageChanged := language != tray.UILanguage()
					if languageChanged {
						tray.SetUILanguage(language)
						refreshMenuLanguage()
						if configurationErr != nil {
							configuration.SetTooltip(tr("无法启动本地配置页面，请使用“设置与工具 → 编辑配置…”"))
						}
					}
					updateState := updates.snapshot()
					checkUpdate.SetTitle(updateMenuTitleForLanguage(updateState, currentLanguage()))
					checkUpdate.SetTooltip(tr(updateState.Error))
					setEnabled(checkUpdate, !updateState.Checking && !updateState.Downloading && !updateState.Installing && !editing && !loggingOut)
					setChecked(autoUpdates, updateState.Automatic)
					if updateState.Available {
						tools.SetTitle(tr("设置与工具 · 有更新"))
					} else {
						tools.SetTitle(tr("设置与工具"))
					}
					ticks++
					if ticks%10 == 0 || languageChanged {
						refreshLoginItem()
					}
					snapshot := controller.Snapshot()
					openReadyLogin(snapshot)
					loginMessage := flow.Message()
					if !languageChanged && reflect.DeepEqual(snapshot, previous) && loginMessage == previousLoginMessage && !editing {
						continue
					}
					previous = snapshot
					previousLoginMessage = loginMessage
					loginStatus.SetTitle(tr("授权页面：") + tr(loginMessage))
					if flow.ShowMessage() {
						loginStatus.Show()
					} else {
						loginStatus.Hide()
					}
					if now := isConnected(snapshot); now != connected {
						connected = now
						setTrayIcon(connected)
					}
					presentation := connectionPresentationForLanguage(snapshot, currentLanguage())
					status.SetTitle("TailSplit · " + presentation.Summary)
					connect.SetTitle(presentation.Action)
					systray.SetTooltip("TailSplit · " + presentation.Summary)
					name, _ := tray.DeviceName(preferences.DeviceName)
					device.SetTitle(tr("设备名称：") + shortMenuTitle(name))
					setChecked(autoName, preferences.DeviceName == "")
					setChecked(splitMode, preferences.RoutingMode != tailsplit.RoutingGlobal)
					setChecked(globalMode, preferences.RoutingMode == tailsplit.RoutingGlobal)
					if preferences.RoutingMode == tailsplit.RoutingGlobal {
						routing.SetTitle(tr("模式：全局代理 · Kill Switch"))
					} else {
						routing.SetTitle(tr("模式：规则分流"))
					}
					setChecked(systemDNS, preferences.DirectDNS == "system")
					setChecked(debugLogging, preferences.DebugLogging)
					if preferences.DirectDNS == "system" {
						dnsMenu.SetTitle(tr("直连 DNS：跟随系统"))
						dnsInfo.SetTitle(tr("物理网卡的原始 DNS"))
					} else {
						dnsMenu.SetTitle(tr("直连 DNS：") + preferences.DirectDNS)
						dnsInfo.SetTitle(tr("固定：") + preferences.DirectDNS)
					}
					autoHostname, _ := tray.DeviceName("")
					autoName.SetTooltip(autoHostname)
					if preferences.DeviceName != "" {
						customName.SetTitle(tr("自定义：") + shortMenuTitle(preferences.DeviceName) + "…")
					} else {
						customName.SetTitle(tr("自定义名称…"))
					}
					if snapshot.NetworkName != "" {
						network.SetTitle(tr("Tailnet：") + snapshot.NetworkName)
					} else {
						network.SetTitle(tr("Tailnet：等待连接"))
					}
					if snapshot.ExitNode != "" {
						exitMenu.SetTitle(tr("出口：") + shortMenuTitle(snapshot.ExitNode))
					} else {
						exitMenu.SetTitle(tr("选择出口节点"))
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
		cancel()
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

// setTrayIcon shows a template image that macOS tints to suit the menu bar or,
// while connected, the green image as it is.
func setTrayIcon(connected bool) {
	icon := trayIcon(connected)
	if connected {
		systray.SetIcon(icon)
	} else {
		systray.SetTemplateIcon(icon, icon)
	}
}

func trayIcon(connected bool) []byte {
	// systray shows a 16-point image; 32 pixels keep Retina sharp.
	var buffer bytes.Buffer
	_ = png.Encode(&buffer, markImage(32, false, connected))
	return buffer.Bytes()
}
