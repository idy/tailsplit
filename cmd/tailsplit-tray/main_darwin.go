//go:build darwin

package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"image/png"
	"io"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/appservice"
	"github.com/idy/tailsplit/internal/appupdate"
	"github.com/idy/tailsplit/internal/configweb"
	"github.com/idy/tailsplit/internal/desktopwindow"
	"github.com/idy/tailsplit/internal/locale"
	"github.com/idy/tailsplit/internal/tray"

	systray "github.com/idy/tailsplit/internal/desktoptray"
	"gopkg.in/yaml.v3"
)

type configurationApply struct {
	ctx      context.Context
	check    func(tray.Snapshot) error
	update   func() error
	direct   bool
	action   string
	revision string
	done     chan error
}

type desktopActionController interface {
	Snapshot() tray.Snapshot
	TrafficExitID() (string, error)
	Start() error
	StartLogin() error
	Stop() error
	Logout() error
	SwitchNetwork() error
	RefreshExitNodes() error
	StartExitLogin(string) error
	LogoutExit(string) error
	AddExit(string, ...string) error
}

func desktopConfigurationBusy(action string, snapshot tray.Snapshot, editing, loggingOut, opening bool) bool {
	return editing || loggingOut || opening || snapshot.State == "authorizing" && action != "disconnect"
}

func (request configurationApply) blocked(editing, loggingOut, browserOpening bool, snapshot tray.Snapshot) bool {
	return desktopConfigurationBusy(request.action, snapshot, editing, loggingOut, browserOpening)
}

// LoginFlow and action preparation belong to the menu's event loop. Only the
// returned controller or login-item operation may run on the settings worker.
func desktopActionOperation(action string, controller desktopActionController, flow *tray.LoginFlow, startup func(bool) error, revision ...string) (func() error, error) {
	action, exit := splitExitAction(action)
	switch action {
	case "add-exit":
		flow.Cancel()
		return func() error { return controller.AddExit(exit, revision...) }, nil
	case "login-exit":
		flow.Cancel()
		return func() error { return prepareDesktopExitLogin(controller, exit) }, nil
	case "logout-exit":
		flow.Cancel()
		return func() error { return controller.LogoutExit(exit) }, nil
	case "connect":
		snapshot := controller.Snapshot()
		if snapshot.Active {
			return nil, nil
		}
		exit, err := controller.TrafficExitID()
		if err != nil {
			return nil, err
		}
		if exit == tailsplit.MainExit {
			if snapshot.BackendState == "NeedsLogin" {
				return nil, requestDesktopLogin(controller, flow)
			}
		} else if snapshot.Exits[exit].BackendState == "NeedsLogin" {
			return nil, requestDesktopExitLogin(controller, flow, exit)
		}
		flow.Cancel()
		return controller.Start, nil
	case "disconnect":
		flow.Cancel()
		return controller.Stop, nil
	case "login":
		if desktopLoginReplacesHelper(controller.Snapshot()) {
			flow.Cancel()
			return func() error { return prepareDesktopLogin(controller) }, nil
		}
		return nil, requestDesktopLogin(controller, flow)
	case "logout":
		flow.Cancel()
		// Signing out never opens a browser; only an explicit network switch
		// requests authorization after its serialized operation completes.
		return controller.Logout, nil
	case "switch-network":
		flow.Cancel()
		return controller.SwitchNetwork, nil
	case "refresh-exits":
		return controller.RefreshExitNodes, nil
	case "startup-on", "startup-off":
		return func() error { return startup(action == "startup-on") }, nil
	default:
		return nil, errors.New("unknown desktop action")
	}
}

func setDesktopStartup(enabled bool, set func(bool) error, status func() tray.LoginItemStatus, openSettings func()) error {
	if err := set(enabled); err != nil {
		return err
	}
	if enabled && status() == tray.LoginItemNeedsApproval {
		finishPanelStartup("startup-on", tray.LoginItemNeedsApproval, openSettings)
		// Startup is a boolean in the window API: pending approval must not be
		// presented as enabled. The native menu also shows the pending status.
		if currentLanguage() == "zh" {
			return errors.New("请在“系统设置 → 通用 → 登录项”中允许 TailSplit，完成登录时启动设置。")
		}
		return errors.New("Allow TailSplit in System Settings → General → Login Items to finish enabling Open at Login.")
	}
	return nil
}

// Report actual startup availability, not just a registration awaiting approval.
func finishPanelStartup(action string, status tray.LoginItemStatus, openSettings func()) bool {
	if action == "startup-on" && status == tray.LoginItemNeedsApproval {
		openSettings()
	}
	return status == tray.LoginItemEnabled
}

func main() {
	executable, err := os.Executable()
	if err != nil {
		return
	}
	directory := filepath.Dir(executable)
	configRoot, _ := os.UserConfigDir()
	defaultConfig := filepath.Join(configRoot, "TailSplit", "config.yaml")
	config := flag.String("config", defaultConfig, "configuration path")
	helper := flag.String("helper", filepath.Join(directory, "tailsplit"), "bundled helper")
	template := flag.String("template", "", "optional initial configuration template override")
	logPath := flag.String("log", filepath.Join(configRoot, "TailSplit", "logs", "tailsplit.log"), "private log file")
	startImmediately := flag.Bool("start", false, "start the proxy explicitly after opening the app")
	loginImmediately := flag.Bool("login", false, "start and open the current device login page when ready")
	noConnect := flag.Bool("no-connect", false, "start disconnected after an update")
	updateFailed := flag.Bool("update-failed", false, "report a failed update and retained app")
	openImmediately := flag.Bool("open", false, "show the configuration window at launch")
	uiListen := flag.String("ui-listen", "", "optional local panel address")
	proofDirectory := flag.String("proof-dir", "", "isolated native UI verification output")
	flag.Parse()
	if err := configureDesktopProof(*proofDirectory); err != nil {
		return
	}
	mygo.App.SetName("TailSplit")
	mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	// Isolated fixture runs must not activate or close the installed desktop.
	if desktopProofMode() {
		mygo.App.SetName("TailSplit UI verification")
	}
	locked := mygo.App.RequestSingleInstanceLock()
	mygo.App.SetName("TailSplit")
	if !locked {
		return
	}
	uiLanguage = locale.NewStore(*config)
	tray.SetUILanguage(currentLanguage())
	desktopwindow.ConfigureApplication(currentLanguage(), tray.ShowAbout)
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
	submit := func(request configurationApply) error {
		request.done = make(chan error, 1)
		select {
		case configurationRequests <- request:
		case <-request.ctx.Done():
			return request.ctx.Err()
		case <-ctx.Done():
			return ctx.Err()
		}
		select {
		case err := <-request.done:
			return err
		case <-request.ctx.Done():
			return request.ctx.Err()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	apply := func(requestCtx context.Context, update func() error, direct bool) error {
		return submit(configurationApply{ctx: requestCtx, update: update, direct: direct})
	}
	var startupEnabled atomic.Bool
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
		Language: uiLanguage, ConfigPath: *config, Listen: *uiListen, Snapshot: controller.Snapshot,
		PingExit: controller.PingExitNode,
		AddExit: func(ctx context.Context, id, revision string) error {
			return submit(configurationApply{ctx: ctx, action: "add-exit:" + id, revision: revision})
		},
		RemoveExit: func(ctx context.Context, id, revision string) error {
			return apply(ctx, func() error { return controller.RemoveExit(id, revision) }, true)
		},
		Apply: func(ctx context.Context, check func(tray.Snapshot) error, update func() error) error {
			return submit(configurationApply{ctx: ctx, check: check, update: update})
		},
		UpdateState: updates.snapshot, UpdateAction: updates.action,
		Action: func(ctx context.Context, action string) error {
			return submit(configurationApply{ctx: ctx, action: action})
		},
		Logs: func() (string, error) { return desktopLogTail(*logPath) },
		// Read the event loop's cached status from browser requests. A login
		// item awaiting system approval must not be reported as enabled.
		Startup: startupEnabled.Load,
	})
	var configurationWindow *desktopwindow.Window
	if configurationPage != nil {
		configurationWindow, err = desktopwindow.New(configurationPage.URL(), true)
		if err != nil {
			controller.RecordDesktopEvent("Could not prepare MyGo window: " + err.Error())
		}
		if configurationWindow != nil {
			go func() {
				if err := configurationWindow.Run(); err != nil {
					controller.RecordDesktopEvent("MyGo window stopped: " + err.Error())
				}
			}()
		}
	}
	showConfiguration := func() {
		if configurationPage != nil {
			if configurationWindow == nil {
				openPath(configurationPage.URL())
				return
			}
			if err := configurationWindow.Show(); err != nil {
				controller.RecordDesktopEvent("Configuration window unavailable; opening the panel in the default browser: " + err.Error())
				openPath(configurationPage.URL())
			}
		}
	}
	setTrayIcon(false)
	systray.SetTooltip("TailSplit")
	runErr := systray.Run(func() {
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
		splitMode := addMenu(routing, "规则分流", "按规则列表和自定义规则决定直连或出口", preferences.RoutingMode != tailsplit.RoutingGlobal)
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
		if *openImmediately || desktopProofMode() {
			showConfiguration()
		}
		startupAction := desktopStartupAction(*config, preferences.ConnectOnStart, *noConnect)
		if *loginImmediately {
			startupAction = "login"
		} else if !*noConnect && *startImmediately {
			startupAction = "connect"
		}
		if desktopProofMode() {
			startupAction = ""
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
			switchingNetwork := false
			// Prepare an existing privileged service before any device startup.
			// This also runs after an update's --no-connect relaunch.
			editing := true
			startupPending := true
			settingsDone := make(chan error, 1)
			var pendingRequest chan error
			pendingAction := startupAction
			if pendingAction == "" {
				pendingAction = "startup-service"
			}
			go func() {
				err := prepareDesktopStartup(ctx, func(ctx context.Context) error {
					// app-run performs its own exact-build readiness check before
					// traffic; only other startup paths need a parent preparation.
					if desktopProofMode() || startupAction == "connect" {
						return nil
					}
					return controller.PrepareService(ctx, func(ctx context.Context) error {
						return appservice.UpgradeExisting(ctx, *helper, io.Discard)
					})
				}, func() error {
					switch startupAction {
					case "login":
						return controller.StartLogin()
					case "connect":
						return controller.Start()
					case "refresh-exits":
						return controller.RefreshExitNodes()
					default:
						return nil
					}
				})
				if err != nil && !errors.Is(err, context.Canceled) {
					controller.RecordDesktopEvent("Desktop startup preparation failed: " + err.Error())
				}
				settingsDone <- err
			}()
			completeRequest := func(err error) bool {
				reply := pendingRequest
				pendingRequest, pendingAction = nil, ""
				if reply == nil {
					return false
				}
				reply <- err
				return true
			}
			reloadPreferences := func() {
				if updated, readErr := tray.ReadPreferences(*config); readErr == nil {
					preferences = updated
					updates.updater.SetAutomatic(updated.CheckForUpdates)
				}
				previous = tray.Snapshot{}
			}
			reconfigure := func(update func() error) {
				editing = true
				flow.Cancel()
				go func() { settingsDone <- controller.Reconfigure(update) }()
			}
			requestLogin := func() error {
				return nativePrimarySignIn(*config,
					func() error { showConfiguration(); return nil },
					func() error {
						operation, err := desktopActionOperation("login", controller, &flow, nil)
						if err != nil || operation == nil {
							return err
						}
						editing, pendingAction = true, "login"
						go func() { settingsDone <- operation() }()
						return nil
					})
			}
			loginItemStatus := tray.LoginItemUnavailable
			refreshLoginItem := func() {
				loginItemStatus = tray.LoginItem()
				startupEnabled.Store(finishPanelStartup("", loginItemStatus, tray.OpenLoginItemSettings))
				setChecked(startAtLogin, loginItemStatus == tray.LoginItemEnabled || loginItemStatus == tray.LoginItemNeedsApproval)
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
				if editing || loggingOut {
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
					if controller.CancelServicePreparation() {
						flow.Cancel()
						continue
					}
					if editing || loggingOut || flow.Opening() {
						continue
					}
					snapshot := controller.Snapshot()
					if snapshot.Active {
						flow.Cancel()
						editing = true
						go func() { settingsDone <- controller.Stop() }()
					} else {
						operation, _ := desktopActionOperation("connect", controller, &flow, nil)
						if operation != nil {
							_ = operation()
						}
						openReadyLogin(controller.Snapshot())
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
				case err := <-logoutDone:
					loggingOut = false
					if err == nil && switchingNetwork {
						reloadPreferences()
						flow.Request()
						openReadyLogin(controller.Snapshot())
					} else if err != nil {
						flow.Fail("无法重新登录，请查看日志")
					}
					switchingNetwork = false
					previous = tray.Snapshot{}
					if !completeRequest(err) && err != nil {
						showError(tr("无法重新登录，请查看日志"))
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
					if !editing && !loggingOut && !flow.Opening() {
						if err := controller.RefreshExitNodes(); err != nil {
							showError(tr("无法刷新出口列表，请检查网络和后台服务"))
						}
					}
				case request := <-configurationRequests:
					if err := request.ctx.Err(); err != nil {
						request.done <- err
						continue
					}
					// Cancellation belongs to the existing startup worker; do not
					// overwrite its pending reply or settingsDone ownership.
					if request.action == "disconnect" && controller.CancelServicePreparation() {
						flow.Cancel()
						request.done <- nil
						continue
					}
					if request.blocked(editing, loggingOut, flow.Opening(), controller.Snapshot()) {
						request.done <- configweb.ErrBusy
						continue
					}
					var operation func() error
					if request.action != "" {
						var err error
						operation, err = desktopActionOperation(request.action, controller, &flow, func(enabled bool) error {
							return setDesktopStartup(enabled, tray.SetLoginItem, tray.LoginItem, tray.OpenLoginItemSettings)
						}, request.revision)
						if err != nil || operation == nil {
							if err == nil {
								openReadyLogin(controller.Snapshot())
							}
							request.done <- err
							continue
						}
					} else {
						flow.Cancel()
						operation = request.update
						if !request.direct {
							operation = func() error { return controller.ReconfigureChecked(request.check, request.update) }
						}
					}
					pendingRequest, pendingAction = request.done, request.action
					if request.action == "logout" || request.action == "switch-network" {
						loggingOut = true
						switchingNetwork = request.action == "switch-network"
						go func() { logoutDone <- operation() }()
					} else {
						editing = true
						go func() { settingsDone <- operation() }()
					}
				case err := <-settingsDone:
					if startupPending && errors.Is(err, context.Canceled) {
						flow.Cancel()
						pendingAction, err = "", nil
					}
					startupPending = false
					editing = false
					reloadPreferences()
					if pendingAction == "startup-on" || pendingAction == "startup-off" {
						// Refresh even after a registration or approval error so the
						// panel sees the actual status before its request completes.
						refreshLoginItem()
					}
					if action, _ := splitExitAction(pendingAction); action == "login-exit" || action == "login" || action == "add-exit" {
						err = finishDesktopExitLogin(&flow, err)
						if err == nil {
							openReadyLogin(controller.Snapshot())
						}
					}
					// Return errors to the panel after clearing the busy state.
					if !completeRequest(err) && err != nil {
						showError(tr("无法应用设置，请检查配置和后台服务"))
					}
				case <-startAtLogin.ClickedCh:
					if editing || loggingOut || flow.Opening() {
						continue
					}
					enable := loginItemStatus != tray.LoginItemEnabled && loginItemStatus != tray.LoginItemNeedsApproval
					if err := tray.SetLoginItem(enable); err != nil {
						showError(tr("无法更新登录项，请从打包的 TailSplit.app 操作或检查系统登录项设置"))
					}
					refreshLoginItem()
					if enable && loginItemStatus == tray.LoginItemNeedsApproval {
						tray.OpenLoginItemSettings()
					}
				case <-connectOnStart.ClickedCh:
					if editing || loggingOut || flow.Opening() {
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
					showConfiguration()
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
					if editing || loggingOut || flow.Opening() || controller.Snapshot().State == "authorizing" {
						continue
					}
					editing = true
					flow.Cancel()
					go func() {
						err := controller.Stop()
						if err == nil {
							openPath("-a", "Installer", filepath.Join(directory, "..", "Resources", "TailSplit Network Service.pkg"))
						}
						settingsDone <- err
					}()
				case <-removeService.ClickedCh:
					if editing || loggingOut || flow.Opening() || controller.Snapshot().State == "authorizing" {
						continue
					}
					editing = true
					flow.Cancel()
					go func() {
						err := controller.Stop()
						if err == nil {
							openPath("-a", "Installer", filepath.Join(directory, "..", "Resources", "Remove TailSplit Network Service.pkg"))
						}
						settingsDone <- err
					}()
				case <-copyAddress.ClickedCh:
					if value := proxyAddress(*config); value != "" {
						command := exec.Command("/usr/bin/pbcopy")
						command.Stdin = strings.NewReader("socks5h://" + value)
						_ = command.Run()
					}
				case <-quit.ClickedCh:
					go func() {
						_ = controller.Close()
						systray.Quit()
					}()
					return
				case <-ticker.C:
					language := currentLanguage()
					languageChanged := language != tray.UILanguage()
					if languageChanged {
						tray.SetUILanguage(language)
						desktopwindow.ConfigureApplication(language, tray.ShowAbout)
						refreshMenuLanguage()
						if configurationErr != nil {
							configuration.SetTooltip(tr("无法启动本地配置页面，请使用“设置与工具 → 编辑配置…”"))
						}
					}
					updateState := updates.snapshot()
					checkUpdate.SetTitle(updateMenuTitleForLanguage(updateState, currentLanguage()))
					checkUpdate.SetTooltip(tr(updateState.Error))
					setEnabled(checkUpdate, !updateState.Busy && !updateState.Confirming && !updateState.Checking && !updateState.Downloading && !updateState.Installing && !editing && !loggingOut)
					setChecked(autoUpdates, updateState.Automatic)
					setEnabled(autoUpdates, !updateState.Busy && !updateState.Confirming && !updateState.Checking && !updateState.Downloading && !updateState.Installing && !editing && !loggingOut)
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
					setEnabled(connect, (!busy || snapshot.PreparingService()) && !flow.Opening())
					setEnabled(restart, canReconnect(snapshot) && !busy && !flow.Opening())
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
					setEnabled(connectOnStart, !busy && !flow.Opening())
					setEnabled(startAtLogin, loginItemStatus != tray.LoginItemUnavailable && !busy && !flow.Opening())
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
	if runErr != nil {
		controller.RecordDesktopEvent("MyGo desktop stopped: " + runErr.Error())
	}
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
	data := tailsplit.DefaultYAML
	var err error
	if template != "" {
		data, err = os.ReadFile(template)
		if err != nil {
			return err
		}
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
