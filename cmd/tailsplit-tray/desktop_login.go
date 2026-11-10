package main

import (
	"errors"
	"strings"

	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/tray"
)

// Connect valid direct-only profiles too. Otherwise reuse saved identities for
// discovery until the user chooses a traffic gateway.
func desktopStartupAction(path string, connectOnStart, noConnect bool) string {
	config, err := tailsplit.Load(path)
	if err != nil {
		return "refresh-exits" // Let the existing status/error path report the file problem.
	}
	if connectOnStart && !noConnect && tailsplit.ValidateTrafficExit(config) == nil {
		return "connect"
	}
	if len(config.ExitIDs()) == 0 {
		return ""
	}
	return "refresh-exits"
}

// Native primary sign-in remains visible for legacy profiles. Without main,
// show the panel's Add exit workflow rather than launching an invalid main login.
func nativePrimarySignIn(path string, openPanel, login func() error) error {
	config, err := tailsplit.Load(path)
	if err != nil {
		return err
	}
	if config.Tailscale == nil {
		return openPanel()
	}
	return login()
}

type desktopLoginController interface {
	Snapshot() tray.Snapshot
	StartLogin() error
	Stop() error
}

type desktopExitLoginController interface {
	Snapshot() tray.Snapshot
	StartExitLogin(string) error
	Stop() error
}

type desktopConnectController interface {
	desktopLoginController
	StartExitLogin(string) error
	TrafficExitID() (string, error)
	Start() error
}

// Both desktop hosts prepare Connect against the selected traffic device. Known
// signed-out devices use the existing login/browser flow before carrying traffic.
// Flow mutations stay on the event loop; the returned Start can run on a worker.
func desktopConnectOperation(controller desktopConnectController, flow *tray.LoginFlow) (func() error, error) {
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
}

// splitExitAction reads per-device add, login and logout actions.
func splitExitAction(action string) (string, string) {
	name, id, ok := strings.Cut(action, ":")
	if !ok || name != "login-exit" && name != "logout-exit" && name != "add-exit" {
		return action, ""
	}
	return name, id
}

var errDesktopGlobalLogin = errors.New("Disconnect Global mode before signing in to another device. This turns off Kill-Switch protection; reconnect manually when ready.")

func desktopGlobalActive(snapshot tray.Snapshot) bool {
	return snapshot.Active && (snapshot.Mode == "tun" || snapshot.Mode == "proxy") && snapshot.RoutingMode == tailsplit.RoutingGlobal
}

func desktopPendingAuthorization(snapshot tray.Snapshot) bool {
	backend := snapshot.BackendState
	if owner := snapshot.LoginExitID(); owner != tailsplit.MainExit {
		backend = snapshot.Exits[owner].BackendState
	}
	return snapshot.LoginURL != "" || backend == "NeedsLogin"
}

// Current-device authorization belongs to the active helper. Replacing it for
// another device requires a separate, explicitly requested Global disconnect.
func prepareDesktopExitLogin(controller desktopExitLoginController, id string) error {
	snapshot := controller.Snapshot()
	if snapshot.Active {
		if snapshot.LoginExitID() == id && desktopPendingAuthorization(snapshot) {
			return nil
		}
		if desktopGlobalActive(snapshot) {
			return errDesktopGlobalLogin
		}
		if err := controller.Stop(); err != nil {
			return err
		}
	}
	return controller.StartExitLogin(id)
}

// Complete on the event loop: workers never mutate LoginFlow or expose helper errors.
func finishDesktopExitLogin(flow *tray.LoginFlow, err error) error {
	if err != nil {
		if errors.Is(err, errDesktopGlobalLogin) {
			flow.Fail(err.Error())
			return err
		}
		if errors.Is(err, tray.ErrConfigChanged) {
			flow.Fail("配置文件已更新，请重新读取后重试。")
			return tray.ErrConfigChanged
		}
		flow.Fail("无法准备登录，请查看日志")
		return errors.New(tr("无法准备登录，请查看日志"))
	}
	flow.Request()
	return nil
}

func requestDesktopExitLogin(controller desktopExitLoginController, flow *tray.LoginFlow, id string) error {
	return finishDesktopExitLogin(flow, prepareDesktopExitLogin(controller, id))
}

// A signed-in helper for another device cannot authorize main. Preserve pending
// authorization from the active device, including its existing browser link.
func desktopLoginReplacesHelper(snapshot tray.Snapshot) bool {
	owner := snapshot.LoginExitID()
	otherSignedIn := owner != tailsplit.MainExit && snapshot.Exits[owner].BackendState == "Running" && snapshot.LoginURL == ""
	return snapshot.Active && (snapshot.Mode == "discovery" || otherSignedIn)
}

// Workers may replace a helper; only the event loop mutates LoginFlow.
func prepareDesktopLogin(controller desktopLoginController) error {
	snapshot := controller.Snapshot()
	if desktopGlobalActive(snapshot) && snapshot.LoginExitID() != tailsplit.MainExit && !desktopPendingAuthorization(snapshot) {
		return errDesktopGlobalLogin
	}
	if desktopLoginReplacesHelper(snapshot) {
		if err := controller.Stop(); err != nil {
			return err
		}
		return controller.StartLogin()
	}
	if !snapshot.Active {
		return controller.StartLogin()
	}
	return nil
}

func requestDesktopLogin(controller desktopLoginController, flow *tray.LoginFlow) error {
	return finishDesktopExitLogin(flow, prepareDesktopLogin(controller))
}
