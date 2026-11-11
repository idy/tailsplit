//go:build windows

package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"image/png"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/egoist/mygo"
	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/appupdate"
	"github.com/idy/tailsplit/internal/configweb"
	"github.com/idy/tailsplit/internal/desktopwindow"
	"github.com/idy/tailsplit/internal/locale"
	"github.com/idy/tailsplit/internal/privatefs"
	"github.com/idy/tailsplit/internal/tray"

	systray "github.com/idy/tailsplit/internal/desktoptray"
	"github.com/tailscale/go-winio"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var version = "dev"
var commit = "unknown"

// Only a control-only update quit sets this process result. Setup consumes it
// immediately; no restart intent is left in user preferences or on disk.
var desktopUpdateShouldReconnect bool

const desktopUpdateReconnectExitCode = 10

type windowsOperation struct {
	ctx   context.Context
	apply func() error
	done  chan error
}

type windowsApp struct {
	ctx                context.Context
	cancel             context.CancelFunc
	controller         *tray.Controller
	configPath         string
	window             *desktopwindow.Window
	panelReady         <-chan struct{}
	showPanel          func() error // Published by closing panelReady.
	operations         chan windowsOperation
	login              tray.LoginFlow
	loginCancellations *desktopLoginCancellations
	browserDone        chan error
	quit               sync.Once
	updates            *desktopUpdates
}

func desktopPipeDescriptor(sid string) string {
	// The GUI owns this listener and needs create-instance access for Accept.
	// Administrator maintenance only needs the fixed show/quit client protocol.
	return "O:" + sid + "D:P(A;;GA;;;SY)(A;;0x12019b;;;BA)(A;;GA;;;" + sid + ")"
}

func main() {
	if err := runWindowsDesktop(); err != nil {
		maintenance := false
		for _, arg := range os.Args[1:] {
			if arg == "--quit" || arg == "-quit" || arg == "--quit-for-update" || arg == "-quit-for-update" {
				maintenance = true
			}
		}
		if maintenance {
			fmt.Fprintln(os.Stderr, err)
		} else {
			windowsError(err.Error())
		}
		os.Exit(1)
	}
	if desktopUpdateShouldReconnect {
		os.Exit(desktopUpdateReconnectExitCode)
	}
}

func runWindowsDesktop() (runErr error) {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	sid := user.User.Sid.String()
	pipeName := `\\.\pipe\im.lbv.tailsplit.desktop.` + sid
	configRoot, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	root := filepath.Join(configRoot, "TailSplit")
	flags := flag.NewFlagSet("TailSplit", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	quit := flags.Bool("quit", false, "gracefully quit the existing desktop app")
	quitForUpdate := flags.Bool("quit-for-update", false, "quit and report connection intent to the update installer")
	background := flags.Bool("background", false, "start in the notification area")
	startImmediately := flags.Bool("start", false, "start the proxy explicitly after opening the app")
	noConnect := flags.Bool("no-connect", false, "start disconnected regardless of the automatic connection preference")
	updateRevision := flags.String("update-revision", "", "restore only the routing document confirmed before an update")
	config := flags.String("config", filepath.Join(root, "config.yaml"), "user configuration")
	logPath := flags.String("log", filepath.Join(root, "logs", "tailsplit.log"), "private log")
	uiListen := flags.String("ui-listen", "", "optional local panel address")
	proofDirectory := flags.String("proof-dir", "", "isolated native UI verification output")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	if err := configureDesktopProof(*proofDirectory); err != nil {
		return err
	}
	uiLanguage = locale.NewStore(*config)
	tray.SetUILanguage(currentLanguage())
	if flags.NArg() != 0 {
		return errors.New("unexpected desktop application argument")
	}
	command := "show"
	if *quit || *quitForUpdate {
		command = "quit"
		if *quitForUpdate {
			if !validDesktopUpdateRevision(*updateRevision) {
				return errors.New("invalid update configuration revision")
			}
			command = "quit-update " + *updateRevision
		}
		// An elevated installer may have been approved by another account. It
		// may only close the registered user's protected app, never start a UI.
		if windows.GetCurrentProcessToken().IsElevated() {
			key, openErr := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\TailSplit`, registry.QUERY_VALUE|registry.WOW64_64KEY)
			if openErr == nil {
				registered, _, readErr := key.GetStringValue("DesktopUserSID")
				key.Close()
				parsed, parseErr := windows.StringToSid(registered)
				if readErr != nil || parseErr != nil || parsed.String() != registered {
					return errors.New("invalid registered desktop user")
				}
				sid = registered
				pipeName = `\\.\pipe\im.lbv.tailsplit.desktop.` + sid
			}
		}
	}
	ctx, cancelRequest := context.WithTimeout(context.Background(), 35*time.Second)
	reconnect, err := desktopRequest(ctx, pipeName, command, executable, sid)
	cancelRequest()
	if err == nil {
		desktopUpdateShouldReconnect = *quitForUpdate && reconnect
		return nil
	}
	if *quit || *quitForUpdate {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
			return nil
		}
		return err
	}
	// Setup launches through the original user's shell. Never turn an elevated
	// terminal into a permanently elevated web UI.
	if windows.GetCurrentProcessToken().IsElevated() {
		return errors.New("Open TailSplit normally from the Start menu, without Run as administrator.")
	}
	listener, err := winio.ListenPipe(pipeName, &winio.PipeConfig{SecurityDescriptor: desktopPipeDescriptor(sid)})
	if err != nil {
		return errors.New("another TailSplit desktop app is starting; open it from the notification area")
	}
	defer listener.Close()
	browser := filepath.Join(root, "browser")
	for _, directory := range []string{root, filepath.Dir(*config), filepath.Dir(*logPath), browser} {
		// WebView2 grants its sandbox access inside its own user data folder,
		// so a later launch must not reject those ACLs as unsafe app state.
		if err := privatefs.EnsureDir(directory, browser); err != nil {
			return err
		}
	}
	if err := initializeWindowsConfig(*config); err != nil {
		return err
	}
	if err := privatefs.CheckFile(*config); err != nil {
		return err
	}
	// SetLocale persists framework preferences. The protected user root must
	// already exist, and control-only --quit must never initialize GUI state.
	mygo.App.SetName("TailSplit")
	desktopwindow.ConfigureApplication(currentLanguage(), tray.ShowAbout)
	controller, err := tray.New(tray.Options{Helper: filepath.Join(filepath.Dir(executable), "tailsplit-core.exe"), ConfigPath: *config, LogPath: *logPath})
	if err != nil {
		return err
	}
	defer controller.Close()
	controller.RecordDesktopEvent("Desktop controller opened; preparing the local window.")
	defer func() {
		if runErr != nil {
			controller.RecordDesktopEvent("Desktop startup failed: " + runErr.Error())
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The tray owns application lifetime, independently of the settings frame,
	// its recovery keeper and any transient popup. Explicit Quit still exits.
	offAllClosed := mygo.App.OnWindowAllClosed(func() {})
	defer offAllClosed()
	// Keep one bounded pending request while the dispatcher handles its status
	// tick. An unbuffered nonblocking send can reject a click while idle.
	panelReady := make(chan struct{})
	app := &windowsApp{ctx: ctx, cancel: cancel, controller: controller, configPath: *config, operations: make(chan windowsOperation, 1), browserDone: make(chan error, 1), loginCancellations: newDesktopLoginCancellations(), panelReady: panelReady}
	go app.operate()
	preferences, err := tray.ReadPreferences(*config)
	if err != nil {
		// Keep the documented default; the window reports the configuration error.
		preferences.CheckForUpdates = true
	}
	app.updates = newDesktopUpdates(ctx, version, preferences.CheckForUpdates,
		func(ctx context.Context, enabled bool) error {
			return app.do(ctx, func() error { return tray.SetCheckForUpdates(*config, enabled) })
		}, func(update appupdate.Package) error {
			revision, err := desktopUpdateRevision(*config, controller.Snapshot())
			if err != nil {
				return err
			}
			return launchWindowsUpdate(update, revision, *config, *logPath)
		})
	var showConfiguration func()
	cancelConnection := app.loginCancellations.callback(controller)
	page, err := configweb.Start(configweb.Options{
		Language: uiLanguage, ConfigPath: *config, Listen: *uiListen, Snapshot: controller.Snapshot,
		PingExit: controller.PingExitNode, ClaimConnectRequest: controller.ClaimConnectRequest,
		ClaimConnectOperation: controller.ClaimConnectOperation,
		ObserveNativeConnectRequest: observeLiveNativeConnectRequests(controller, panelReady, func() bool {
			return app.window != nil && app.window.CanReceiveConnectRequest()
		}),
		ClaimPanelConnectOperation: controller.ClaimPanelConnectOperation,
		Cancel:                     cancelConnection,
		AddExit: func(ctx context.Context, id, revision string) error {
			return app.do(ctx, func() error {
				app.login.Cancel()
				scoped := controller.ForOperation(tray.OperationID(ctx))
				err := scoped.AddExit(id, revision)
				app.loginCancellations.apply(&app.login)
				return finishDesktopExitLogin(&app.login, err, desktopLoginIdentity(scoped)...)
			})
		},
		RemoveExit: func(ctx context.Context, id, revision string) error {
			return app.do(ctx, func() error {
				app.login.Cancel()
				return controller.RemoveExit(id, revision)
			})
		},
		Apply: func(ctx context.Context, check func(tray.Snapshot) error, update func() error) error {
			return app.do(ctx, func() error {
				err := controller.ReconfigureOperation(tray.OperationID(ctx), check, update)
				if preferences, readErr := tray.ReadPreferences(*config); readErr == nil {
					app.updates.updater.SetAutomatic(preferences.CheckForUpdates)
				}
				return err
			})
		},
		Connect: func(ctx context.Context, revision string) error {
			return app.do(ctx, func() error {
				scoped := controller.ForOperation(tray.OperationID(ctx))
				if err := scoped.Check(); err != nil {
					return err
				}
				operation, err := checkedDesktopConnectOperation(*config, revision, scoped, &app.login)
				if err != nil || operation == nil {
					return err
				}
				err = operation()
				app.loginCancellations.apply(&app.login)
				return finishDesktopConnect(&app.login, err, desktopLoginIdentity(scoped)...)
			})
		},
		UpdateState: app.updates.snapshot, UpdateAction: app.updates.action,
		Action: app.action, Logs: func() (string, error) { return desktopLogTail(*logPath) },
		Startup: func() bool { return tray.LoginItem() == tray.LoginItemEnabled },
		OpenPanel: func() {
			select {
			case <-panelReady:
				showConfiguration()
			case <-ctx.Done():
			}
		},
		Quit: systray.Quit,
	})
	if err != nil {
		return err
	}
	defer page.Close()
	app.window, err = desktopwindow.New(page.URL(), *background)
	if err != nil {
		return err
	}
	app.window.SetCancelHandler(cancelConnection)
	app.window.SetConnectRequestHandler(func() { controller.OfferNativeConnectRequest(app.window.CanPresentConnectRequest) })
	fallback := configurationFallback{ctx: ctx, launch: func() {
		if err := mygo.Shell.OpenExternal(tray.ConfigurationWindowConnectURL(page.URL(), controller.Snapshot().ConnectRequest)); err != nil {
			windowsError(err.Error())
		}
	}}
	reportWindowFailure := func(err error) {
		controller.RecordDesktopEvent("Native configuration window unavailable: " + err.Error())
	}
	app.showPanel = func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		fallback.beginShow()
		fallback.nativeFailure(app.window.Show(), true, reportWindowFailure)
		return nil
	}
	showConfiguration = func() { _ = app.showConfiguration() }
	close(panelReady)
	go func() {
		controller.RecordDesktopEvent("MyGo native window starting with the system WebView.")
		if err := app.window.Run(); err != nil {
			fallback.nativeFailure(err, app.window.WantsVisible(), reportWindowFailure)
		}
	}()
	go app.serveDesktop(listener)
	systray.SetPopupURL(page.PopupURL())
	systray.SetIcon(windowsTrayIcon(false))
	systray.SetTooltip("TailSplit")
	return systray.Run(func() {
		open := addMenu("打开 TailSplit", "连接、账号、分流与日志")
		status := addMenu("TailSplit · 未连接", "")
		status.Disable()
		connect := addMenu("连接", "连接或断开 TailSplit")
		systray.AddSeparator()
		exit := addMenu("退出 TailSplit", "先断开网络，再退出")
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		var connected bool
		preferences, _ := tray.ReadPreferences(*config)
		// Load the saved device's login and exits even when startup must stay disconnected.
		go func() {
			if action := desktopStartupAction(*config, preferences.ConnectOnStart || *startImmediately, *noConnect); action != "" && !desktopProofMode() {
				if action == "connect" {
					// Automatic startup reuses identities but never opens a login browser.
					if err := app.do(ctx, func() error { return startDesktopConnection(controller, *updateRevision) }); err != nil {
						controller.RecordDesktopEvent("Desktop startup connection failed: " + err.Error())
					}
				} else {
					_ = app.action(ctx, action)
				}
			}
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case <-open.ClickedCh:
				showConfiguration()
			case <-connect.ClickedCh:
				action := "connect"
				if controller.Snapshot().Active {
					action = "disconnect"
				}
				go app.menuAction(action)
			case <-exit.ClickedCh:
				systray.Quit()
				return
			case <-ticker.C:
				language := currentLanguage()
				languageChanged := language != tray.UILanguage()
				if languageChanged {
					tray.SetUILanguage(language)
					desktopwindow.ConfigureApplication(language, tray.ShowAbout)
					refreshMenuLanguage()
				}
				snapshot := controller.Snapshot()
				presentation := connectionPresentationForLanguage(snapshot, language)
				status.SetTitle("TailSplit · " + presentation.Summary)
				connect.SetTitle(presentation.Action)
				if snapshot.State == "stopping" || snapshot.State == "logging-out" {
					connect.Disable()
				} else {
					connect.Enable()
				}
				systray.SetTooltip("TailSplit · " + presentation.Summary)
				if now := isConnected(snapshot); now != connected {
					connected = now
					systray.SetIcon(windowsTrayIcon(connected))
				}
			}
		}
	}, func() { cancel(); _ = controller.Close(); _ = page.Close() })
}

func (a *windowsApp) showConfiguration() error {
	if a.panelReady == nil {
		return desktopwindow.ErrNotReady
	}
	select {
	case <-a.panelReady:
		if err := a.ctx.Err(); err != nil {
			return err
		}
		if a.showPanel == nil {
			return desktopwindow.ErrNotReady
		}
		return a.showPanel()
	case <-a.ctx.Done():
		return a.ctx.Err()
	}
}

func (a *windowsApp) do(ctx context.Context, apply func() error) error {
	request := windowsOperation{ctx: ctx, apply: apply, done: make(chan error, 1)}
	select {
	case <-a.ctx.Done():
		return errors.New("TailSplit is closing")
	case <-ctx.Done():
		return ctx.Err()
	case a.operations <- request:
	default:
		return configweb.ErrBusy
	}
	select {
	case err := <-request.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-a.ctx.Done():
		return errors.New("TailSplit is closing")
	}
}

func (a *windowsApp) operate() {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-a.loginCancellations.events():
			a.loginCancellations.apply(&a.login)
		case request := <-a.operations:
			a.loginCancellations.apply(&a.login)
			if err := request.ctx.Err(); err != nil {
				request.done <- err
			} else if a.login.Opening() {
				// Cancel leaves Opening set until the browser completion arrives.
				// Do not let that completion overwrite a newer login or switch.
				request.done <- configweb.ErrBusy
			} else {
				err := request.apply()
				a.loginCancellations.apply(&a.login)
				request.done <- err
			}
		case err := <-a.browserDone:
			a.loginCancellations.apply(&a.login)
			a.login.Complete(err)
		case <-ticker.C:
			a.loginCancellations.apply(&a.login)
			if request := a.login.NextBrowserRequest(a.controller.Snapshot()); request.URL != "" {
				go func(request tray.LoginBrowserRequest) {
					ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
					defer cancel()
					err := tray.OpenLoginPageGuarded(ctx, request.URL, func(start func() error) error {
						return a.controller.AuthorizeLoginBrowserStart(request, start)
					})
					a.controller.RecordLoginBrowserResult(err)
					select {
					case a.browserDone <- err:
					case <-a.ctx.Done():
					}
				}(request)
			}
		}
	}
}

func (a *windowsApp) action(ctx context.Context, action string) error {
	return a.do(ctx, func() error {
		controller := a.controller.ForOperation(tray.OperationID(ctx))
		if err := controller.Check(); err != nil {
			return err
		}
		action, exit := splitExitAction(action)
		switch action {
		case "add-exit":
			a.login.Cancel()
			err := controller.AddExit(exit)
			a.loginCancellations.apply(&a.login)
			return finishDesktopExitLogin(&a.login, err, desktopLoginIdentity(controller)...)
		case "login-exit":
			return requestDesktopExitLogin(controller, &a.login, exit)
		case "logout-exit":
			a.login.Cancel()
			return a.controller.LogoutExit(exit)
		case "connect":
			operation, err := desktopConnectOperation(controller, &a.login)
			if err != nil || operation == nil {
				return err
			}
			err = operation()
			a.loginCancellations.apply(&a.login)
			return finishDesktopConnect(&a.login, err, desktopLoginIdentity(controller)...)
		case "disconnect":
			a.login.Cancel()
			return a.controller.Stop()
		case "login":
			return nativePrimarySignIn(a.configPath, a.showConfiguration,
				func() error { return requestDesktopLogin(controller, &a.login) })
		case "logout":
			a.login.Cancel()
			return a.controller.Logout()
		case "switch-network":
			a.login.Cancel()
			if err := controller.SwitchNetwork(); err != nil {
				return err
			}
			a.loginCancellations.apply(&a.login)
			return requestDesktopAuthorization(&a.login, desktopLoginIdentity(controller)...)
		case "refresh-exits":
			return controller.RefreshExitNodes()
		case "startup-on":
			return tray.SetLoginItem(true)
		case "startup-off":
			return tray.SetLoginItem(false)
		default:
			return errors.New("unknown desktop action")
		}
	})
}

func (a *windowsApp) menuAction(action string) {
	if action == "connect" && !a.controller.Snapshot().Active {
		a.controller.RequestConnectionSetup()
		if err := a.showConfiguration(); err != nil {
			windowsError(err.Error())
		}
		return
	}
	ctx, cancel := context.WithTimeout(a.ctx, 45*time.Second)
	defer cancel()
	if err := a.action(ctx, action); err != nil {
		windowsError(err.Error())
	}
}

func (a *windowsApp) shutdown() {
	a.quit.Do(func() {
		go func() {
			// Close prevents a racing queued command from starting another engine.
			_ = a.controller.Close()
			a.cancel()
			_ = a.window.Quit()
			systray.Quit()
		}()
	})
}

func (a *windowsApp) serveDesktop(listener net.Listener) {
	defer listener.Close()
	go func() { <-a.ctx.Done(); _ = listener.Close() }()
	for {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer connection.Close()
			_ = connection.SetDeadline(time.Now().Add(35 * time.Second))
			command, err := bufio.NewReader(io.LimitReader(connection, 128)).ReadString('\n')
			if err != nil {
				return
			}
			switch {
			case command == "show\n":
				if a.showConfiguration() != nil {
					return
				}
				_, _ = io.WriteString(connection, "ok\n")
			case strings.HasPrefix(command, "quit-update "):
				revision := strings.TrimSuffix(strings.TrimPrefix(command, "quit-update "), "\n")
				if !validDesktopUpdateRevision(revision) {
					return
				}
				ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
				err := retryDesktopUpdate(ctx, func() error {
					return a.do(ctx, func() error {
						return a.controller.CloseForUpdate(revision, func(reconnect bool) error {
							response := "ok\n"
							if reconnect {
								response = "ok-start\n"
							}
							_, err := io.WriteString(connection, response)
							return err
						})
					})
				})
				cancel()
				if err != nil {
					a.controller.RecordDesktopEvent("Update shutdown refused: " + err.Error())
					return
				}
				a.shutdown()
			case command == "quit\n":
				if a.controller.Close() != nil {
					return
				}
				_, _ = io.WriteString(connection, "ok\n")
				a.shutdown()
			}
		}()
	}
}

func desktopRequest(ctx context.Context, pipe, command, executable, expectedUser string) (bool, error) {
	connection, err := winio.DialPipeAccess(ctx, pipe, 0x12019b)
	if err != nil {
		return false, err
	}
	defer connection.Close()
	file, ok := connection.(interface{ Fd() uintptr })
	if !ok {
		return false, errors.New("invalid desktop control channel")
	}
	var pid uint32
	if err := windows.GetNamedPipeServerProcessId(windows.Handle(file.Fd()), &pid); err != nil {
		return false, err
	}
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, pid)
	if err != nil {
		return false, err
	}
	defer windows.CloseHandle(process)
	var token windows.Token
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token); err != nil {
		return false, err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil || user.User.Sid.String() != expectedUser {
		return false, errors.New("desktop control channel has an unexpected owner")
	}
	path := make([]uint16, 32768)
	size := uint32(len(path))
	if err := windows.QueryFullProcessImageName(process, 0, &path[0], &size); err != nil {
		return false, err
	}
	if !strings.EqualFold(windows.UTF16ToString(path[:size]), executable) {
		return false, errors.New("another program owns the TailSplit desktop channel")
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	if _, err := io.WriteString(connection, command+"\n"); err != nil {
		return false, err
	}
	response, err := bufio.NewReader(io.LimitReader(connection, 16)).ReadString('\n')
	if err != nil {
		return false, err
	}
	reconnect := strings.HasPrefix(command, "quit-update ") && response == "ok-start\n"
	if response != "ok\n" && !reconnect {
		return false, errors.New("desktop operation was not acknowledged")
	}
	if command == "quit" || strings.HasPrefix(command, "quit-update ") {
		wait, err := windows.WaitForSingleObject(process, 30000)
		if err != nil {
			return false, err
		}
		if wait != windows.WAIT_OBJECT_0 {
			return false, errors.New("TailSplit is still closing; do not replace its running files")
		}
	}
	return reconnect, nil
}

func initializeWindowsConfig(path string) error {
	if _, err := os.Lstat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	file, err := privatefs.CreateFile(path)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(tailsplit.DefaultYAML)
	return errors.Join(writeErr, file.Sync(), file.Close())
}

func windowsError(message string) {
	text, _ := windows.UTF16PtrFromString(tr(message))
	title, _ := windows.UTF16PtrFromString("TailSplit")
	windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x10)
}

// MyGo consumes the same 64px PNG that the migrated adapter selected from the
// former ICO. Executable icon resources keep their separate multi-size artwork.
func windowsTrayIcon(connected bool) []byte {
	var icon bytes.Buffer
	_ = png.Encode(&icon, markImage(64, true, connected))
	return icon.Bytes()
}
