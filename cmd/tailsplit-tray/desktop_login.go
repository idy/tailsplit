package main

import (
	"errors"

	"github.com/idy/tailsplit/internal/tray"
)

type desktopLoginController interface {
	Snapshot() tray.Snapshot
	StartLogin() error
}

// Called on the desktop event loop. An active helper owns the authorization
// URL and any TUN protection; requesting its browser flow must not stop it.
func requestDesktopLogin(controller desktopLoginController, flow *tray.LoginFlow) error {
	if !controller.Snapshot().Active {
		if err := controller.StartLogin(); err != nil {
			flow.Fail("无法准备登录，请查看日志")
			return errors.New(tr("无法准备登录，请查看日志"))
		}
	}
	flow.Request()
	return nil
}
