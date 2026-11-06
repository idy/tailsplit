package tray

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
)

// LoginFlow is owned by the UI event loop. A click can precede the new auth
// URL after logout; queue that intent instead of silently discarding it.
type LoginFlow struct {
	pending     bool
	opening     bool
	message     string
	showMessage bool
}

func (f *LoginFlow) Request() {
	if f.opening {
		return
	}
	f.pending = true
	f.showMessage = true
	f.message = "正在准备新授权链接"
}

func (f *LoginFlow) Cancel() {
	f.pending = false
	f.showMessage = false
	f.message = "未打开"
}

func (f *LoginFlow) Next(snapshot Snapshot) string {
	if !f.pending || f.opening {
		return ""
	}
	if snapshot.State == "failed" {
		f.pending = false
		f.showMessage = true
		f.message = "连接失败，请查看日志"
		return ""
	}
	if snapshot.BackendState == "Direct" {
		f.pending = false
		f.showMessage = true
		f.message = "当前配置无需设备登录"
		return ""
	}
	if snapshot.BackendState == "Running" && snapshot.LoginURL == "" {
		f.pending = false
		f.showMessage = false
		f.message = "设备已登录"
		return ""
	}
	if !validLoginURL(snapshot.LoginURL) {
		return ""
	}
	f.pending, f.opening = false, true
	f.showMessage = true
	f.message = "正在打开默认浏览器"
	return snapshot.LoginURL
}

func (f *LoginFlow) Complete(err error) {
	f.opening = false
	if err != nil {
		f.showMessage = true
		f.message = "打开失败，请复制登录链接"
	} else {
		f.showMessage = true
		f.message = "已请求浏览器打开"
	}
}

func (f *LoginFlow) Fail(message string) {
	f.pending, f.opening = false, false
	f.showMessage = true
	f.message = message
}

// ShowMessage is presentation state, independent of the translated text.
func (f *LoginFlow) ShowMessage() bool { return f.showMessage }

func (f *LoginFlow) Opening() bool { return f.opening }

func (f *LoginFlow) Message() string {
	if f.message == "" {
		return "未打开"
	}
	return f.message
}

// OpenLoginPage asks LaunchServices for a fresh browser instance. Sending a
// URL to an arbitrary existing Chrome instance can route it into a headless
// automation profile instead of opening the user's normal browser window.
func OpenLoginPage(ctx context.Context, value string) error {
	return runBrowserOpen(ctx, func() error {
		if runtime.GOOS == "windows" {
			return openWindowsLogin(ctx, value)
		}
		return openLoginPage(ctx, value, defaultBrowserID(), func(ctx context.Context, name string, args ...string) error {
			// Do not copy command output or URL arguments into logs: the link
			// contains transient authentication material.
			return exec.CommandContext(ctx, name, args...).Run()
		})
	})
}

// Shell/DDE calls cannot be interrupted by a context. Bound the request instead;
// a late native completion goes only to this private buffer, never a newer flow.
func runBrowserOpen(ctx context.Context, open func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- open() }()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
}

type browserCommand func(context.Context, string, ...string) error

func openLoginPage(ctx context.Context, value, browserID string, run browserCommand) error {
	if !validLoginURL(value) {
		return errors.New("no valid Tailscale device login link is available")
	}
	args := []string{"-n", value}
	switch browserID {
	case "com.google.Chrome", "com.google.chrome", "com.google.Chrome.beta", "com.google.Chrome.canary":
		// Pass the URL as Chrome's argv, so its normal-profile singleton
		// opens a visible window rather than sending an AppleEvent to one
		// of the unrelated headless Chrome instances on this machine.
		args = []string{"-n", "-b", browserID, "--args", "--new-window", value}
	}
	if err := run(ctx, "/usr/bin/open", args...); err != nil {
		return errors.New("could not open the default browser; copy the login link from the menu")
	}
	return nil
}
