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
	Snapshot() tray.Snapshot
	TrafficExitID() (string, error)
	Start() error
	StartRevision(string) error
}

// The traffic helper owns policy preflight and keeps its connection intent while
// the selected device authenticates. Cached NeedsLogin on an optional identity
// must not divert Connect into a separate login-only helper.
func desktopConnectOperation(controller desktopConnectController, flow *tray.LoginFlow) (func() error, error) {
	snapshot := controller.Snapshot()
	if snapshot.Active {
		return nil, nil
	}
	if _, err := controller.TrafficExitID(); err != nil {
		return nil, err
	}
	flow.Cancel()
	return controller.Start, nil
}

// Complete on the event loop after Start has cleared stale identity metadata.
// Only explicit Connect requests use this; automatic startup never opens login.
func finishDesktopConnect(flow *tray.LoginFlow, err error, operation ...string) error {
	if err != nil {
		cancelDesktopAuthorization(flow, operation...)
		return err
	}
	return requestDesktopAuthorization(flow, operation...)
}

func cancelDesktopAuthorization(flow *tray.LoginFlow, operation ...string) {
	if len(operation) > 0 && operation[0] != "" {
		flow.CancelOperation(operation[0])
		return
	}
	flow.Cancel()
}

func requestDesktopAuthorization(flow *tray.LoginFlow, operation ...string) error {
	if len(operation) == 0 || operation[0] == "" {
		flow.Request()
		return nil
	}
	if flow.OperationCanceled(operation[0]) {
		return tray.ErrOperationCanceled
	}
	if !flow.RequestOperation(operation[0], operation[1:]...) && len(operation) > 1 && flow.OperationCanceled(operation[1]) {
		return tray.ErrOperationCanceled
	}
	return nil
}

func desktopLoginIdentity(controller interface{ Snapshot() tray.Snapshot }) []string {
	owner := controller.Snapshot().OperationID
	operation := owner
	if scoped, ok := controller.(*tray.Operation); ok {
		operation = scoped.ID
	}
	return []string{operation, owner}
}

// checkedDesktopConnectOperation runs on the operation gate and checks again on
// the worker before starting the traffic helper and its authentication preflight.
func checkedDesktopConnectOperation(path, revision string, controller desktopConnectController, flow *tray.LoginFlow) (func() error, error) {
	if err := tray.CheckConfigRevision(path, revision); err != nil {
		return nil, err
	}
	operation, err := desktopConnectOperation(controller, flow)
	if err != nil || operation == nil {
		return operation, err
	}
	return func() error {
		if err := tray.CheckConfigRevision(path, revision); err != nil {
			return err
		}
		return controller.StartRevision(revision)
	}, nil
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
func finishDesktopExitLogin(flow *tray.LoginFlow, err error, operation ...string) error {
	if err != nil {
		if errors.Is(err, tray.ErrOperationCanceled) {
			cancelDesktopAuthorization(flow, operation...)
			return err
		}
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
	return requestDesktopAuthorization(flow, operation...)
}

func requestDesktopExitLogin(controller desktopExitLoginController, flow *tray.LoginFlow, id string) error {
	err := prepareDesktopExitLogin(controller, id)
	return finishDesktopExitLogin(flow, err, desktopLoginIdentity(controller)...)
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
	err := prepareDesktopLogin(controller)
	return finishDesktopExitLogin(flow, err, desktopLoginIdentity(controller)...)
}
