package main

import (
	"errors"

	"github.com/idy/tailsplit/internal/tray"
)

type desktopLoginController interface {
	Snapshot() tray.Snapshot
	StartLogin() error
	Stop() error
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
