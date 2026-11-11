package tray

import (
	"errors"

	"github.com/idy/tailsplit"
)

var errUpdateGlobal = errors.New("explicitly disconnect Global before updating; this releases Kill-Switch protection, and you can reconnect after the update")

// updateShutdown keeps the sampled intent cancellable until restart is committed.
// All access, including the final callback, is serialized by Controller.mu.
type updateShutdown struct {
	reconnect bool
	committed bool
}

// CloseForUpdate closes the controller before committing an update restart. Only
// a live traffic helper may request reconnect, and its immutable source revision
// must still match both expectedRevision and the saved document before shutdown.
// Active Global sessions require an explicit disconnect: the desktop cannot
// retain their TUN through replacement or guarantee restoration if an exit goes
// offline. Preflight errors leave the helper and its protection untouched.
//
// A matching cancellation during cleanup clears reconnect. If the saved document
// changes during cleanup, commit still runs with reconnect=false. commit runs
// under the controller lock, must be short, and must not reenter the Controller.
// A successful commit rejects later cancellation because it cannot revoke restart.
func (c *Controller) CloseForUpdate(expectedRevision string, commit func(reconnect bool) error) error {
	if commit == nil {
		return errors.New("update commit callback is required")
	}
	c.mu.Lock()
	if c.closed || c.updateShutdown != nil {
		c.mu.Unlock()
		return ErrOperationCanceled
	}
	traffic := c.command != nil && (c.snapshot.Mode == "tun" || c.snapshot.Mode == "proxy")
	if traffic && c.snapshot.RoutingMode == tailsplit.RoutingGlobal {
		c.mu.Unlock()
		return errUpdateGlobal
	}
	reconnect := traffic && !c.stopping && !c.loggingOut &&
		c.snapshot.State != "stopping" && c.snapshot.State != "failed" && c.snapshot.State != "logging-out"
	if reconnect {
		if err := c.checkUpdatePolicyLocked(expectedRevision); err != nil {
			c.mu.Unlock()
			return err
		}
	}
	shutdown := &updateShutdown{reconnect: reconnect}
	c.updateShutdown = shutdown
	// Seal start admission under the same lock as the sample. Close performs the
	// existing cleanup without retaining this lock, so cancellation can join it.
	c.closed = true
	c.mu.Unlock()
	if err := c.Close(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if shutdown.reconnect {
		policy, err := readUpdatePolicy(c.options.ConfigPath)
		if err != nil || policy.token != expectedRevision {
			shutdown.reconnect = false
		}
	}
	if err := commit(shutdown.reconnect); err != nil {
		return err
	}
	shutdown.committed = true
	return nil
}
