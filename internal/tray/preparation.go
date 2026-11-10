package tray

import (
	"context"
	"errors"

	"github.com/idy/tailsplit"
)

const servicePreparationMode = "service-preparation"

type servicePreparation struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// PrepareService publishes startup authorization through the ordinary snapshot
// and owns a cancelable operation independently of any network helper process.
func (c *Controller) PrepareService(ctx context.Context, prepare func(context.Context) error) error {
	return c.prepareService(ctx, "", prepare)
}

func (c *Controller) prepareService(ctx context.Context, operation string, prepare func(context.Context) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if prepare == nil {
		return errors.New("service preparation is required")
	}
	c.mu.Lock()
	if err := c.operationAllowedLocked(operation); err != nil {
		c.mu.Unlock()
		return err
	}
	if c.closed || c.stopping || c.reconfiguring || c.loggingOut || c.command != nil || c.pendingAdd != nil || c.preparation != nil || c.pingCancel != nil {
		c.mu.Unlock()
		return tailsplit.ErrPingBusy
	}
	runCtx, cancel := context.WithCancel(ctx)
	op := &servicePreparation{cancel: cancel, done: make(chan struct{})}
	previousState, previousMode, previousError, previousCode := c.snapshot.State, c.snapshot.Mode, c.snapshot.Error, c.snapshot.ErrorCode
	c.preparation = op
	c.ownOperationLocked(operation)
	c.snapshot.State, c.snapshot.Mode, c.snapshot.Error, c.snapshot.ErrorCode = "authorizing", servicePreparationMode, "", ""
	c.mu.Unlock()

	err := prepare(runCtx)
	c.mu.Lock()
	// A cancellation racing a successful probe must not start the next action.
	if err == nil {
		err = runCtx.Err()
	}
	cancel()
	c.preparation = nil
	c.snapshot.State, c.snapshot.Mode, c.snapshot.Error, c.snapshot.ErrorCode = previousState, previousMode, previousError, previousCode
	if err == nil && previousError == "后台网络服务准备失败，请重试" {
		c.snapshot.State, c.snapshot.Mode, c.snapshot.Error, c.snapshot.ErrorCode = "stopped", "", "", ""
	}
	if err != nil && !errors.Is(err, context.Canceled) && !c.closed {
		c.snapshot.State = "failed"
		c.snapshot.Error = "后台网络服务准备失败，请重试"
		c.snapshot.ErrorCode = "service-installation"
		c.RecordDesktopEvent("Network service preparation failed: " + err.Error())
	}
	close(op.done)
	c.mu.Unlock()
	return err
}

// CancelServicePreparation requests cancellation without waiting on the UI
// thread. The preparation worker retains the operation gate until it exits.
func (c *Controller) CancelServicePreparation() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.preparation == nil {
		return false
	}
	c.markCanceledLocked(c.operationID)
	c.preparation.cancel()
	c.snapshot.State = "stopping"
	return true
}

func (s Snapshot) PreparingService() bool {
	return s.Active && s.Mode == servicePreparationMode
}
