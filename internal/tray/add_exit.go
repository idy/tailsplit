package tray

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"

	"github.com/idy/tailsplit"
)

type pendingExitAddition struct {
	id            string
	path          string
	revision      string
	data          []byte
	authenticated bool
}

// AddExit authenticates a new device using a private temporary configuration.
// The saved document and visible device list change only after authentication
// and graceful helper shutdown. Cancellation never publishes a provisional exit.
func (c *Controller) AddExit(id string, expectedRevision ...string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.stopping || c.reconfiguring || c.loggingOut || c.command != nil || c.pendingAdd != nil || c.preparation != nil {
		return tailsplit.ErrPingBusy
	}
	if len(expectedRevision) > 1 {
		return errors.New("expected at most one configuration revision")
	}
	expected := ""
	if len(expectedRevision) == 1 {
		expected = expectedRevision[0]
	}
	candidate, revision, err := PrepareExitAddition(c.options.ConfigPath, id, expected)
	if err != nil {
		return err
	}
	// Keep the exact validated private file instead of writing a second copy
	// after validation. It must remain available until the helper has exited.
	path, _, err := configCandidate(c.options.ConfigPath, candidate)
	if err != nil {
		return err
	}
	c.pendingAdd = &pendingExitAddition{id: id, path: path, revision: revision, data: candidate}
	if err := c.startConfigLocked("login-new", id, path); err != nil {
		c.pendingAdd = nil
		_ = os.Remove(path)
		return err
	}
	return nil
}

// Called with the controller lock after its helper has exited and released the
// device lease. Only the terminal authenticated event may authorize publication.
func (c *Controller) finishPendingAddLocked(helperErr error) {
	pending := c.pendingAdd
	if pending == nil {
		return
	}
	defer func() { _ = os.Remove(pending.path); c.pendingAdd = nil }()
	if helperErr != nil || c.closed || c.stopping || !pending.authenticated || c.snapshot.State == "failed" {
		delete(c.snapshot.Exits, pending.id)
		return
	}
	if err := SaveConfigDocument(c.options.ConfigPath, pending.revision, pending.data); err != nil {
		delete(c.snapshot.Exits, pending.id)
		c.snapshot.State = "failed"
		c.snapshot.Error = "已完成登录，但无法保存出口。请重新读取配置后重试。"
		c.RecordDesktopEvent("Could not commit the authenticated exit: " + err.Error())
		return
	}
	c.snapshot.AddedExit = pending.id
	c.snapshot.AddedRevision = fmt.Sprintf("%x", sha256.Sum256(pending.data))
}
