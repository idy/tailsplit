//go:build windows

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
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

	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/configweb"
	"github.com/idy/tailsplit/internal/locale"
	"github.com/idy/tailsplit/internal/privatefs"
	"github.com/idy/tailsplit/internal/tray"
	"github.com/idy/tailsplit/internal/winui"

	"github.com/getlantern/systray"
	"github.com/tailscale/go-winio"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var version = "dev"
var commit = "unknown"

type windowsOperation struct {
	ctx   context.Context
	apply func() error
	done  chan error
}

type windowsApp struct {
	ctx         context.Context
	cancel      context.CancelFunc
	controller  *tray.Controller
	window      *winui.Window
	operations  chan windowsOperation
	login       tray.LoginFlow
	browserDone chan error
	quit        sync.Once
	updates     *desktopUpdates
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
			if arg == "--quit" || arg == "-quit" {
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
	background := flags.Bool("background", false, "start in the notification area")
	noConnect := flags.Bool("no-connect", false, "start disconnected after installation")
	config := flags.String("config", filepath.Join(root, "config.yaml"), "user configuration")
	logPath := flags.String("log", filepath.Join(root, "logs", "tailsplit.log"), "private log")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	uiLanguage = locale.NewStore(*config)
	tray.SetUILanguage(currentLanguage())
	if flags.NArg() != 0 {
		return errors.New("unexpected desktop application argument")
	}
	command := "show"
	if *quit {
		command = "quit"
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
	err = desktopRequest(ctx, pipeName, command, executable, sid)
	cancelRequest()
	if err == nil {
		return nil
	}
	if *quit {
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
	// Keep one bounded pending request while the dispatcher handles its status
	// tick. An unbuffered nonblocking send can reject a click while idle.
	app := &windowsApp{ctx: ctx, cancel: cancel, controller: controller, operations: make(chan windowsOperation, 1), browserDone: make(chan error, 1)}
	go app.operate()
	preferences, err := tray.ReadPreferences(*config)
	if err != nil {
		// Keep the documented default; the window reports the configuration error.
		preferences.CheckForUpdates = true
	}
	app.updates = newDesktopUpdates(ctx, version, preferences.CheckForUpdates,
		func(ctx context.Context, enabled bool) error {
			return app.do(ctx, func() error { return tray.SetCheckForUpdates(*config, enabled) })
		}, launchWindowsUpdate)
	page, err := configweb.Start(configweb.Options{
		Language: uiLanguage, ConfigPath: *config, Snapshot: controller.Snapshot,
		PingExit: controller.PingExitNode,
		RemoveExit: func(ctx context.Context, id, revision string) error {
			return app.do(ctx, func() error {
				app.login.Cancel()
				return controller.RemoveExit(id, revision)
			})
		},
		Apply: func(ctx context.Context, update func() error) error {
			return app.do(ctx, func() error {
				err := controller.Reconfigure(update)
				if preferences, readErr := tray.ReadPreferences(*config); readErr == nil {
					app.updates.updater.SetAutomatic(preferences.CheckForUpdates)
				}
				return err
			})
		},
		UpdateState: app.updates.snapshot, UpdateAction: app.updates.action,
		Action: app.action, Logs: func() (string, error) { return desktopLogTail(*logPath) },
		Startup: func() bool { return tray.LoginItem() == tray.LoginItemEnabled },
	})
	if err != nil {
		return err
	}
	defer page.Close()
	app.window, err = winui.New(filepath.Join(filepath.Dir(executable), "TailSplit.Window.dll"), page.URL(), browser, "TailSplit.Window."+sid)
	if err != nil {
		return err
	}
	windowDone := make(chan error, 1)
	go func() {
		controller.RecordDesktopEvent("Native window host loaded; starting WebView2.")
		windowDone <- app.window.Run()
	}()
	go func() {
		select {
		case err := <-windowDone:
			if err != nil {
				controller.RecordDesktopEvent("Native window initialization failed: " + err.Error())
				windowsError("Could not open the TailSplit window. Repair TailSplit and its Microsoft Edge WebView2 runtime.\n\n" + err.Error())
			}
			app.shutdown()
		case <-ctx.Done():
		}
	}()
	go app.serveDesktop(listener)
	systray.Run(func() {
		systray.SetIcon(windowsTrayIcon(false))
		systray.SetTooltip("TailSplit")
		open := addMenu(nil, "打开 TailSplit", "连接、账号、分流与日志")
		status := addMenu(nil, "未连接", "")
		status.Disable()
		connect := addMenu(nil, "连接", "使用当前配置连接")
		disconnect := addMenu(nil, "断开", "停止网络会话并恢复受管理的网络")
		login := addMenu(nil, "登录 Tailnet…", "在默认浏览器中授权本机设备")
		refresh := addMenu(nil, "刷新出口节点", "只读取设备，不创建 TUN")
		startup := addMenu(nil, "登录 Windows 后启动", "只启动界面；自动连接另行设置", tray.LoginItem() == tray.LoginItemEnabled)
		systray.AddSeparator()
		checkUpdate := addMenu(nil, "检查更新…", "检查 GitHub 正式版；安装前会确认")
		autoUpdates := addMenu(nil, "自动检查更新", "启动后和每六小时检查", preferences.CheckForUpdates)
		about := addMenu(nil, "关于 TailSplit", "版本与源代码信息")
		exit := addMenu(nil, "退出 TailSplit", "先断开网络，再退出")
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		var connected bool
		if *background {
			go hideInitialWindowsWindow("TailSplit.Window."+sid, ctx)
		}
		preferences, _ := tray.ReadPreferences(*config)
		// Load the saved device's login and exits even when startup must stay disconnected.
		go func() {
			if action := desktopStartupAction(*config, preferences.ConnectOnStart, *noConnect); action != "" {
				_ = app.action(ctx, action)
			}
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case <-open.ClickedCh:
				_ = app.window.Show()
			case <-connect.ClickedCh:
				go app.menuAction("connect")
			case <-disconnect.ClickedCh:
				go app.menuAction("disconnect")
			case <-login.ClickedCh:
				go app.menuAction("login")
			case <-refresh.ClickedCh:
				go app.menuAction("refresh-exits")
			case <-startup.ClickedCh:
				action := "startup-on"
				if tray.LoginItem() == tray.LoginItemEnabled {
					action = "startup-off"
				}
				go app.menuAction(action)
			case <-checkUpdate.ClickedCh:
				action := "check"
				if app.updates.snapshot().Available {
					action = "install"
				}
				go func() {
					if err := app.updates.action(ctx, action); err != nil {
						windowsError(err.Error())
					}
				}()
			case <-autoUpdates.ClickedCh:
				action := "automatic-on"
				if app.updates.snapshot().Automatic {
					action = "automatic-off"
				}
				go func() {
					if err := app.updates.action(ctx, action); err != nil {
						windowsError(err.Error())
					}
				}()
			case <-about.ClickedCh:
				windowsError(fmt.Sprintf("TailSplit %s\n%s%s\nhttps://github.com/idy/tailsplit", version, tr("源代码："), commit))
			case <-exit.ClickedCh:
				app.shutdown()
				return
			case <-ticker.C:
				language := currentLanguage()
				languageChanged := language != tray.UILanguage()
				if languageChanged {
					tray.SetUILanguage(language)
					refreshMenuLanguage()
				}
				updateState := app.updates.snapshot()
				checkUpdate.SetTitle(updateMenuTitleForLanguage(updateState, currentLanguage()))
				checkUpdate.SetTooltip(tr(updateState.Error))
				if updateState.Checking || updateState.Downloading || updateState.Installing {
					checkUpdate.Disable()
				} else {
					checkUpdate.Enable()
				}
				if updateState.Automatic {
					autoUpdates.Check()
				} else {
					autoUpdates.Uncheck()
				}
				snapshot := controller.Snapshot()
				presentation := connectionPresentationForLanguage(snapshot, language)
				status.SetTitle("TailSplit · " + presentation.Summary)
				systray.SetTooltip("TailSplit · " + presentation.Summary)
				if now := isConnected(snapshot); now != connected {
					connected = now
					systray.SetIcon(windowsTrayIcon(connected))
				}
				if tray.LoginItem() == tray.LoginItemEnabled {
					startup.Check()
				} else {
					startup.Uncheck()
				}
			}
		}
	}, func() { cancel(); _ = controller.Close(); _ = page.Close() })
	return nil
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
		case request := <-a.operations:
			if err := request.ctx.Err(); err != nil {
				request.done <- err
			} else if a.login.Opening() {
				// Cancel leaves Opening set until the browser completion arrives.
				// Do not let that completion overwrite a newer login or switch.
				request.done <- configweb.ErrBusy
			} else {
				request.done <- request.apply()
			}
		case err := <-a.browserDone:
			a.login.Complete(err)
		case <-ticker.C:
			if link := a.login.Next(a.controller.Snapshot()); link != "" {
				go func() {
					ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
					defer cancel()
					err := tray.OpenLoginPage(ctx, link)
					a.controller.RecordLoginBrowserResult(err)
					select {
					case a.browserDone <- err:
					case <-a.ctx.Done():
					}
				}()
			}
		}
	}
}

func (a *windowsApp) action(ctx context.Context, action string) error {
	return a.do(ctx, func() error {
		action, exit := splitExitAction(action)
		switch action {
		case "login-exit":
			return requestDesktopExitLogin(a.controller, &a.login, exit)
		case "logout-exit":
			a.login.Cancel()
			return a.controller.LogoutExit(exit)
		case "connect":
			return a.controller.Start()
		case "disconnect":
			a.login.Cancel()
			return a.controller.Stop()
		case "login":
			return requestDesktopLogin(a.controller, &a.login)
		case "logout":
			a.login.Cancel()
			return a.controller.Logout()
		case "switch-network":
			a.login.Cancel()
			if err := a.controller.SwitchNetwork(); err != nil {
				return err
			}
			a.login.Request()
			return nil
		case "refresh-exits":
			return a.controller.RefreshExitNodes()
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
			command, err := bufio.NewReader(io.LimitReader(connection, 16)).ReadString('\n')
			if err != nil {
				return
			}
			switch command {
			case "show\n":
				if a.window.Show() != nil {
					return
				}
				_, _ = io.WriteString(connection, "ok\n")
			case "quit\n":
				if a.controller.Close() != nil {
					return
				}
				_, _ = io.WriteString(connection, "ok\n")
				a.shutdown()
			}
		}()
	}
}

func desktopRequest(ctx context.Context, pipe, command, executable, expectedUser string) error {
	connection, err := winio.DialPipeAccess(ctx, pipe, 0x12019b)
	if err != nil {
		return err
	}
	defer connection.Close()
	file, ok := connection.(interface{ Fd() uintptr })
	if !ok {
		return errors.New("invalid desktop control channel")
	}
	var pid uint32
	if err := windows.GetNamedPipeServerProcessId(windows.Handle(file.Fd()), &pid); err != nil {
		return err
	}
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, pid)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(process)
	var token windows.Token
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token); err != nil {
		return err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil || user.User.Sid.String() != expectedUser {
		return errors.New("desktop control channel has an unexpected owner")
	}
	path := make([]uint16, 32768)
	size := uint32(len(path))
	if err := windows.QueryFullProcessImageName(process, 0, &path[0], &size); err != nil {
		return err
	}
	if !strings.EqualFold(windows.UTF16ToString(path[:size]), executable) {
		return errors.New("another program owns the TailSplit desktop channel")
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	if _, err := io.WriteString(connection, command+"\n"); err != nil {
		return err
	}
	response, err := bufio.NewReader(io.LimitReader(connection, 16)).ReadString('\n')
	if err != nil {
		return err
	}
	if response != "ok\n" {
		return errors.New("desktop operation was not acknowledged")
	}
	if command == "quit" {
		wait, err := windows.WaitForSingleObject(process, 30000)
		if err != nil {
			return err
		}
		if wait != windows.WAIT_OBJECT_0 {
			return errors.New("TailSplit is still closing; do not replace its running files")
		}
	}
	return nil
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

func hideInitialWindowsWindow(class string, ctx context.Context) {
	name, _ := windows.UTF16PtrFromString(class)
	user32 := windows.NewLazySystemDLL("user32.dll")
	for attempt := 0; attempt < 100; attempt++ {
		handle, _, _ := user32.NewProc("FindWindowW").Call(uintptr(unsafe.Pointer(name)), 0)
		if handle != 0 {
			user32.NewProc("PostMessageW").Call(handle, 0x10, 0, 0)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func windowsError(message string) {
	text, _ := windows.UTF16PtrFromString(tr(message))
	title, _ := windows.UTF16PtrFromString("TailSplit")
	windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x10)
}

// windowsTrayIconSizes cover the system icon metric from 100% to 200% scaling;
// LoadImage picks the closest one instead of scaling a single image.
var windowsTrayIconSizes = []int{16, 20, 24, 32, 40, 48, 64}

func windowsTrayIcon(connected bool) []byte {
	pictures := make([][]byte, len(windowsTrayIconSizes))
	for i, size := range windowsTrayIconSizes {
		var picture bytes.Buffer
		_ = png.Encode(&picture, markImage(size, true, connected))
		pictures[i] = picture.Bytes()
	}
	var icon bytes.Buffer
	_ = binary.Write(&icon, binary.LittleEndian, [3]uint16{0, 1, uint16(len(pictures))})
	offset := 6 + 16*len(pictures)
	for i, size := range windowsTrayIconSizes {
		icon.Write([]byte{byte(size), byte(size), 0, 0})
		_ = binary.Write(&icon, binary.LittleEndian, [2]uint16{1, 32})
		_ = binary.Write(&icon, binary.LittleEndian, [2]uint32{uint32(len(pictures[i])), uint32(offset)})
		offset += len(pictures[i])
	}
	for _, picture := range pictures {
		icon.Write(picture)
	}
	return icon.Bytes()
}
