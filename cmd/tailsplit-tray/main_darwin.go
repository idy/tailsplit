//go:build darwin

package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
	StartRevision(string) error
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

// LoginFlow and action preparation belong to the desktop event loop. Only the
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
		return desktopConnectOperation(controller, flow)
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
		// presented as enabled.
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
	uninstallerPath := filepath.Join(directory, "..", "Resources", "Remove TailSplit Network Service.pkg")
	uninstallService := func() {
		go func() {
			err := apply(ctx, func() error {
				return removeDesktopNetworkService(controller.Stop, func() error {
					return exec.Command("/usr/bin/open", "-a", "Installer", uninstallerPath).Run()
				})
			}, true)
			if err != nil {
				controller.RecordDesktopEvent("Could not open network-service removal: " + err.Error())
				showError(tr("无法卸载后台服务，请查看日志"))
			}
		}()
	}
	configureApplication := func() {
		if _, err := os.Stat(uninstallerPath); err == nil {
			desktopwindow.ConfigureApplication(currentLanguage(), tray.ShowAbout, uninstallService)
		} else {
			desktopwindow.ConfigureApplication(currentLanguage(), tray.ShowAbout)
		}
	}
	configureApplication()
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
	var showConfiguration func()
	panelReady := make(chan struct{})
	configurationPage, configurationErr := configweb.Start(configweb.Options{
		Language: uiLanguage, ConfigPath: *config, Listen: *uiListen, Snapshot: controller.Snapshot,
		CompanionUpdates: true,
		PingExit:         controller.PingExitNode,
		AddExit: func(ctx context.Context, id, revision string) error {
			return submit(configurationApply{ctx: ctx, action: "add-exit:" + id, revision: revision})
		},
		RemoveExit: func(ctx context.Context, id, revision string) error {
			return apply(ctx, func() error { return controller.RemoveExit(id, revision) }, true)
		},
		Apply: func(ctx context.Context, check func(tray.Snapshot) error, update func() error) error {
			return submit(configurationApply{ctx: ctx, check: check, update: update})
		},
		Connect: func(ctx context.Context, revision string) error {
			return submit(configurationApply{ctx: ctx, action: "connect", revision: revision})
		},
		UpdateState: updates.snapshot, UpdateAction: updates.action,
		Action: func(ctx context.Context, action string) error {
			return submit(configurationApply{ctx: ctx, action: action})
		},
		Logs: func() (string, error) { return desktopLogTail(*logPath) },
		// Read the event loop's cached status from browser requests. A login
		// item awaiting system approval must not be reported as enabled.
		Startup: startupEnabled.Load,
		OpenPanel: func() {
			select {
			case <-panelReady:
				showConfiguration()
			case <-ctx.Done():
			}
		},
		Quit: systray.Quit,
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
	showConfiguration = func() {
		if configurationPage == nil {
			openPath("-t", *config)
			return
		}
		if configurationWindow == nil {
			openPath(configurationPage.URL())
			return
		}
		if err := configurationWindow.Show(); err != nil {
			controller.RecordDesktopEvent("Configuration window unavailable; opening the panel in the default browser: " + err.Error())
			openPath(configurationPage.URL())
		}
	}
	close(panelReady)
	if configurationPage != nil {
		systray.SetPopupURL(configurationPage.PopupURL())
	}
	setTrayIcon(false)
	systray.SetTooltip("TailSplit")
	runErr := systray.Run(func() {
		configurationLabel, configurationTip := "打开 TailSplit", "打开 TailSplit 配置面板"
		if configurationErr != nil {
			configurationLabel, configurationTip = "编辑配置…", "编辑配置…"
		}
		configuration := addMenu(configurationLabel, configurationTip)
		status := addMenu("TailSplit · 未连接", "")
		status.Disable()
		connect := addMenu("连接", "连接或断开 TailSplit")
		var removeService *systray.MenuItem
		var removeServiceClicks <-chan struct{}
		if _, err := os.Stat(uninstallerPath); err == nil {
			removeService = addMenu("卸载后台服务…", "停止代理并打开卸载包；保留登录和配置")
			removeServiceClicks = removeService.ClickedCh
		}
		systray.AddSeparator()
		quit := addMenu("退出 TailSplit", "同时停止此 App 启动的代理")
		if *openImmediately || desktopProofMode() && os.Getenv("TAILSPLIT_TRAY_PROOF_DIR") == "" {
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
			ticker := time.NewTicker(500 * time.Millisecond)
			defer ticker.Stop()
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
			}
			refreshLoginItem := func() {
				startupEnabled.Store(finishPanelStartup("", tray.LoginItem(), tray.OpenLoginItemSettings))
			}
			refreshLoginItem()
			ticks := 0
			openReadyLogin := func(snapshot tray.Snapshot) {
				if editing || loggingOut {
					return
				}
				if value := flow.Next(snapshot); value != "" {
					go func() {
						ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
						defer cancel()
						browserDone <- tray.OpenLoginPage(ctx, value)
					}()
				}
			}
			for {
				select {
				case <-ctx.Done():
					return
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
						operation, err := desktopActionOperation("connect", controller, &flow, nil)
						if err != nil {
							showError(tr("无法应用设置，请检查配置和后台服务"))
							continue
						}
						if operation != nil {
							editing = true
							pendingAction = "connect"
							go func() { settingsDone <- operation() }()
						} else {
							openReadyLogin(controller.Snapshot())
						}
					}
				case err := <-browserDone:
					controller.RecordLoginBrowserResult(err)
					flow.Complete(err)
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
					if !completeRequest(err) && err != nil {
						showError(tr("无法重新登录，请查看日志"))
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
						if request.action == "connect" && request.revision != "" {
							operation, err = checkedDesktopConnectOperation(*config, request.revision, controller, &flow)
						} else {
							operation, err = desktopActionOperation(request.action, controller, &flow, func(enabled bool) error {
								return setDesktopStartup(enabled, tray.SetLoginItem, tray.LoginItem, tray.OpenLoginItemSettings)
							}, request.revision)
						}
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
					userConnect := pendingAction == "connect" && !startupPending
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
					if userConnect {
						err = finishDesktopConnect(&flow, err)
						if err == nil {
							openReadyLogin(controller.Snapshot())
						}
					}
					// Return errors to the panel after clearing the busy state.
					if !completeRequest(err) && err != nil {
						showError(tr("无法应用设置，请检查配置和后台服务"))
					}
				case <-configuration.ClickedCh:
					showConfiguration()
				case <-removeServiceClicks:
					uninstallService()
				case <-quit.ClickedCh:
					systray.Quit()
					return
				case <-ticker.C:
					language := currentLanguage()
					languageChanged := language != tray.UILanguage()
					if languageChanged {
						tray.SetUILanguage(language)
						configureApplication()
						refreshMenuLanguage()
					}
					ticks++
					if ticks%10 == 0 || languageChanged {
						refreshLoginItem()
					}
					snapshot := controller.Snapshot()
					openReadyLogin(snapshot)
					if now := isConnected(snapshot); now != connected {
						connected = now
						setTrayIcon(connected)
					}
					presentation := connectionPresentationForLanguage(snapshot, currentLanguage())
					status.SetTitle("TailSplit · " + presentation.Summary)
					connect.SetTitle(presentation.Action)
					systray.SetTooltip("TailSplit · " + presentation.Summary)
					busy := editing || loggingOut || snapshot.State == "stopping" || snapshot.State == "logging-out"
					setEnabled(connect, (!busy || snapshot.PreparingService()) && !flow.Opening())
					if removeService != nil {
						setEnabled(removeService, !busy && !flow.Opening() && snapshot.State != "authorizing")
					}
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
