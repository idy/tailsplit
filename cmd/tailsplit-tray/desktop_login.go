package main

import (
	"errors"
	"strings"

	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/tray"
)

// A fresh profile has no device to start. Reuse saved identities for discovery
// until the user chooses a traffic gateway, even when auto-connect is enabled.
func desktopStartupAction(path string, connectOnStart, noConnect bool) string {
	config, err := tailsplit.Load(path)
	if err != nil {
		return "refresh-exits" // Let the existing status/error path report the file problem.
	}
	if len(config.ExitIDs()) == 0 {
		return ""
	}
	if connectOnStart && !noConnect && tailsplit.ValidateTrafficExit(config) == nil {
		return "connect"
	}
	return "refresh-exits"
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

// splitExitAction reads "login-exit:<id>" and "logout-exit:<id>" actions.
func splitExitAction(action string) (string, string) {
	name, id, ok := strings.Cut(action, ":")
	if !ok || name != "login-exit" && name != "logout-exit" {
		return action, ""
	}
	return name, id
}

// An additional exit shares the running core's state lock, so its login
// replaces any active session; the user reconnects after choosing a node.
func prepareDesktopExitLogin(controller desktopExitLoginController, id string) error {
	if controller.Snapshot().Active {
		if err := controller.Stop(); err != nil {
			return err
		}
	}
	return controller.StartExitLogin(id)
}

// Complete on the event loop: workers never mutate LoginFlow or expose helper errors.
func finishDesktopExitLogin(flow *tray.LoginFlow, err error) error {
	if err != nil {
		flow.Fail("无法准备登录，请查看日志")
		return errors.New(tr("无法准备登录，请查看日志"))
	}
	flow.Request()
	return nil
}

func requestDesktopExitLogin(controller desktopExitLoginController, flow *tray.LoginFlow, id string) error {
	return finishDesktopExitLogin(flow, prepareDesktopExitLogin(controller, id))
}

// Called on the desktop event loop. Discovery cannot supply authorization URLs,
// so replace only that listener-free helper. Keep active connections and
// interactive login helpers intact when requesting their browser flow.
func requestDesktopLogin(controller desktopLoginController, flow *tray.LoginFlow) error {
	snapshot := controller.Snapshot()
	if snapshot.Active && snapshot.Mode == "discovery" {
		if err := controller.Stop(); err != nil {
			flow.Fail("无法准备登录，请查看日志")
			return errors.New(tr("无法准备登录，请查看日志"))
		}
		snapshot = controller.Snapshot()
	}
	if !snapshot.Active {
		if err := controller.StartLogin(); err != nil {
			flow.Fail("无法准备登录，请查看日志")
			return errors.New(tr("无法准备登录，请查看日志"))
		}
	}
	flow.Request()
	return nil
}
