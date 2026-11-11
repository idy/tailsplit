package tray

import (
	"context"
	"errors"
	"os/exec"
	"runtime"

	"github.com/idy/tailsplit"
)

// ErrLoginURLChanged rejects only a stale browser selection. The same live
// operation may retry its latest authorization URL without losing ownership.
var ErrLoginURLChanged = errors.New("登录链接已更新")

// LoginFlow is owned by the UI event loop. A click can precede the new auth
// URL after logout; queue that intent instead of silently discarding it.
type LoginFlow struct {
	pending            bool
	opening            bool
	abandoned          bool // UI intent ended; distinct from revoking its helper owner.
	message            string
	showMessage        bool
	operationID        string
	ownerID            string
	canceledOperations map[string]bool
}

// RequestOperation binds this browser intent to its action and the helper
// supplying authorization. All methods remain owned by the desktop event loop.
func (f *LoginFlow) RequestOperation(operation string, owner ...string) bool {
	if operation == "" {
		f.Request()
		return f.pending
	}
	ownerID := operation
	if len(owner) > 0 {
		ownerID = owner[0]
	}
	if f.canceledOperations[operation] || f.canceledOperations[ownerID] || f.opening {
		return false
	}
	f.operationID, f.ownerID = operation, ownerID
	f.request()
	return true
}

func (f *LoginFlow) OperationCanceled(operation string) bool { return f.canceledOperations[operation] }

// Retain revocation even before an asynchronous action completion requests its
// browser intent. A delayed cancellation must not clear a different newer flow.
func (f *LoginFlow) CancelOperation(operation string) bool {
	if operation == "" {
		return false
	}
	if f.canceledOperations == nil {
		f.canceledOperations = make(map[string]bool)
	}
	f.canceledOperations[operation] = true
	if f.operationID != operation && f.ownerID != operation {
		return false
	}
	if f.operationID != "" {
		f.canceledOperations[f.operationID] = true
	}
	f.Cancel()
	return true
}

func (f *LoginFlow) Request() {
	if f.opening {
		return
	}
	f.operationID, f.ownerID = "", ""
	f.request()
}

func (f *LoginFlow) request() {
	if f.opening {
		return
	}
	f.pending = true
	f.abandoned = false
	f.showMessage = true
	f.message = "正在准备新授权链接"
}

func (f *LoginFlow) Cancel() {
	f.abandoned = true
	f.pending = false
	f.showMessage = false
	f.message = "未打开"
}

func (f *LoginFlow) Next(snapshot Snapshot) string {
	if !f.pending || f.opening {
		return ""
	}
	if f.operationID != "" && (f.canceledOperations[f.operationID] || snapshot.OperationID != f.ownerID) {
		f.CancelOperation(f.operationID)
		return ""
	}
	if snapshot.State == "failed" {
		f.pending = false
		f.showMessage = true
		f.message = "连接失败，请查看日志"
		return ""
	}
	// The login helper can finish and clear Focus between polling ticks. Its
	// terminal result belongs to this intent regardless of the primary's state.
	completed := snapshot.Mode == "login" && snapshot.State == "authenticated"
	backend := snapshot.BackendState
	if exit := snapshot.LoginExitID(); exit != tailsplit.MainExit {
		backend = snapshot.Exits[exit].BackendState
	}
	if backend == "NeedsMachineAuth" {
		f.showMessage = true
		f.message = "等待 Tailnet 管理员批准此设备"
		return ""
	}
	if backend == "Direct" && !completed {
		f.pending = false
		f.showMessage = true
		f.message = "当前配置无需设备登录"
		return ""
	}
	if (completed || backend == "Running") && snapshot.LoginURL == "" {
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

// LoginBrowserRequest is copied on the owning event loop before dispatching a
// worker. Its identities must be checked again at the actual OS Start boundary.
type LoginBrowserRequest struct {
	URL         string
	OperationID string
	OwnerID     string
}

func (f *LoginFlow) NextBrowserRequest(snapshot Snapshot) LoginBrowserRequest {
	value := f.Next(snapshot)
	if value == "" {
		return LoginBrowserRequest{}
	}
	return LoginBrowserRequest{URL: value, OperationID: f.operationID, OwnerID: f.ownerID}
}

func (f *LoginFlow) Complete(err error) {
	f.opening = false
	if errors.Is(err, ErrOperationCanceled) {
		// A launch can also be rejected during an internal helper transition.
		// Only explicit CancelOperation notifications revoke owner identities.
		f.Cancel()
		return
	}
	if f.operationID != "" && (f.canceledOperations[f.operationID] || f.canceledOperations[f.ownerID]) {
		f.Cancel()
		return
	}
	if f.abandoned {
		return
	}
	if errors.Is(err, ErrLoginURLChanged) {
		f.request()
		return
	}
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
	return OpenLoginPageGuarded(ctx, value, nil)
}

// BrowserStartGuard must call start synchronously, and may hold ownership only
// around this bounded process.Start callback. Process.Wait always runs outside.
type BrowserStartGuard func(start func() error) error

func OpenLoginPageGuarded(ctx context.Context, value string, guard BrowserStartGuard) error {
	return runBrowserOpen(ctx, func() error {
		if runtime.GOOS == "windows" {
			return openWindowsLoginGuarded(ctx, value, guard)
		}
		return openLoginPage(ctx, value, defaultBrowserID(), func(ctx context.Context, name string, args ...string) error {
			// Do not copy command output or URL arguments into logs: the link
			// contains transient authentication material.
			return runBrowserCommand(ctx, exec.CommandContext(ctx, name, args...), guard)
		})
	})
}

func runBrowserCommand(ctx context.Context, command *exec.Cmd, guard BrowserStartGuard) error {
	if _, err := prepareProcess(command, false); err != nil {
		return err
	}
	start := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return command.Start()
	}
	var err error
	if guard != nil {
		err = guard(start)
	} else {
		err = start()
	}
	if err != nil {
		return err
	}
	return command.Wait()
}

// Admission and the actual OS process start share cancellation's controller
// lock. A successful start commits the dispatch; later cancellation cannot undo
// it. Neither process completion nor the browser timeout holds this lock.
func (c *Controller) AuthorizeLoginBrowserStart(request LoginBrowserRequest, start func() error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if request.OperationID == "" || request.OwnerID == "" || start == nil ||
		c.operationAllowedLocked(request.OperationID) != nil || c.operationAllowedLocked(request.OwnerID) != nil ||
		c.closed || c.stopping || c.reconfiguring || c.loggingOut || c.preparation != nil || c.command == nil ||
		c.operationID != request.OwnerID || c.snapshot.State == "failed" {
		return ErrOperationCanceled
	}
	if request.URL != c.snapshot.LoginURL || !validLoginURL(request.URL) {
		return ErrLoginURLChanged
	}
	// Discovery can outlive the selected device's login state. Match Snapshot's
	// live traffic owner while locked; rejecting this attempt does not revoke it.
	snapshot := c.snapshot
	snapshot.TrafficExit = c.trafficExit
	backend := snapshot.BackendState
	if exit := snapshot.LoginExitID(); exit != tailsplit.MainExit {
		backend = snapshot.Exits[exit].BackendState
	}
	if backend == "NeedsMachineAuth" {
		return ErrOperationCanceled
	}
	return start()
}

// Native browser discovery or dispatch can outlive its caller's context. Bound
// the request; late completion goes only to this private buffer, never a newer flow.
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
		if errors.Is(err, ErrOperationCanceled) || errors.Is(err, ErrLoginURLChanged) {
			return err
		}
		return errors.New("could not open the default browser; copy the login link from the menu")
	}
	return nil
}
