package tray

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/idy/tailsplit"
)

var ErrOperationCanceled = errors.New("操作已取消或被新的操作替代")

type operationContextKey struct{}

// WithOperationID carries a caller-assigned identity through the desktop queue.
func WithOperationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, operationContextKey{}, id)
}

func OperationID(ctx context.Context) string {
	id, _ := ctx.Value(operationContextKey{}).(string)
	return id
}

// Operation keeps one logical owner across preparation, helper replacement and
// guided login/connect phases. Legacy callers receive a fresh identity too.
// Creating a handle does not admit it or replace the current owner.
type Operation struct {
	*Controller
	ID       string
	previous string
}

func (c *Controller) ForOperation(id string) *Operation {
	if id == "" {
		id = rand.Text()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return &Operation{Controller: c, ID: id, previous: c.operationID}
}

func (c *Controller) operationAllowedLocked(id string) error {
	if id != "" && (c.canceledOperations[id] || c.revokedOperations[id]) {
		return ErrOperationCanceled
	}
	return nil
}

func (c *Controller) ownOperationLocked(id string) {
	if id == "" {
		id = rand.Text()
	}
	if c.operationID != "" && c.operationID != id {
		c.markCanceledLocked(c.operationID)
	}
	c.operationID = id
}

func (c *Controller) markCanceledLocked(id string) {
	if id == "" {
		return
	}
	if c.canceledOperations == nil {
		c.canceledOperations = make(map[string]bool)
	}
	c.canceledOperations[id] = true
}

func (o *Operation) Check() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.operationAllowedLocked(o.ID)
}

func (o *Operation) Start() error      { return o.start("proxy", "") }
func (o *Operation) StartLogin() error { return o.start("login", tailsplit.MainExit) }
func (o *Operation) StartExitLogin(id string) error {
	if id == "" {
		id = tailsplit.MainExit
	}
	return o.start("login", id)
}
func (o *Operation) RefreshExitNodes() error {
	c := o.Controller
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.operationAllowedLocked(o.ID); err != nil {
		return err
	}
	if c.command != nil || c.loggingOut || c.preparation != nil {
		return nil
	}
	if c.reconfiguring {
		return tailsplit.ErrPingBusy
	}
	return c.startOperationLocked("discovery", "", o.ID)
}
func (o *Operation) start(mode, focus string) error {
	c := o.Controller
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.operationAllowedLocked(o.ID); err != nil {
		return err
	}
	if c.reconfiguring {
		return tailsplit.ErrPingBusy
	}
	return c.startOperationLocked(mode, focus, o.ID)
}
func (o *Operation) StartRevision(revision string) error {
	return o.Controller.startRevision(revision, o.ID)
}
func (o *Operation) StartUpdateRevision(revision string) error {
	return o.Controller.startUpdateRevision(revision, o.ID)
}
func (o *Operation) AddExit(id string, revision ...string) error {
	return o.Controller.addExit(id, o.ID, revision...)
}
func (o *Operation) PrepareService(ctx context.Context, prepare func(context.Context) error) error {
	return o.Controller.prepareService(ctx, o.ID, prepare)
}

// Stop is an internal phase transition, not cancellation of this operation.
// Recheck the captured owner before replacing any helper, under the same lock
// used by cancellation and start admission.
func (o *Operation) Stop() error {
	c := o.Controller
	c.mu.Lock()
	if err := c.operationAllowedLocked(o.ID); err != nil {
		c.mu.Unlock()
		return err
	}
	if c.operationID != o.ID && c.operationID != o.previous {
		c.mu.Unlock()
		return ErrOperationCanceled
	}
	if c.action != nil {
		c.mu.Unlock()
		return tailsplit.ErrPingBusy
	}
	c.ownOperationLocked(o.ID)
	stop := c.captureStopLocked()
	c.mu.Unlock()
	return c.waitStopped(stop)
}
func (o *Operation) Logout() error              { return o.LogoutExit("") }
func (o *Operation) LogoutExit(id string) error { return o.Controller.logoutExit(id, o) }
func (o *Operation) SwitchNetwork() error {
	save, err := prepareNetworkSwitch(o.options.ConfigPath)
	if err != nil {
		return err
	}
	if err := o.Logout(); err != nil {
		return err
	}
	if err := o.Controller.ReconfigureOperation(o.ID, nil, save); err != nil {
		return err
	}
	return o.StartLogin()
}

// Cancel targets the explicit operation and the retained native-request binding.
// A stale snapshot cannot hide an admitted native owner, and neither identity can
// stop a different, newer owner. Unknown IDs only retain admission tombstones.
func (c *Controller) Cancel(request, operation string) error {
	return c.CancelWithNotify(request, operation, nil)
}

// CancelWithNotify publishes the exact revoked identities before waiting for
// cleanup. Native hosts enqueue these for their login-flow event loop; notify
// must not mutate that event-loop-owned flow from the cancellation goroutine.
func (c *Controller) CancelWithNotify(request, operation string, notify func([]string)) error {
	c.mu.Lock()
	if c.updateShutdown != nil && c.updateShutdown.committed {
		c.mu.Unlock()
		return ErrOperationCanceled
	}
	bound := c.requestOperations[request]
	targets := []string{}
	addTarget := func(id string) {
		if id == "" {
			return
		}
		for _, previous := range targets {
			if previous == id {
				return
			}
		}
		targets = append(targets, id)
	}
	addTarget(operation)
	if bound != "" {
		addTarget(request)
		addTarget(bound)
	}
	if request != "" {
		if c.revokedRequests == nil {
			c.revokedRequests = make(map[string]bool)
		}
		c.revokedRequests[request] = true
		if bound := c.requestOperations[request]; bound != "" {
			if c.revokedOperations == nil {
				c.revokedOperations = make(map[string]bool)
			}
			c.revokedOperations[request] = true
			c.revokedOperations[bound] = true
		}
		if c.connectRequest == request {
			c.connectRequest = ""
		}
	}
	c.markCanceledLocked(operation)
	matched := operation != "" && c.operationID == operation || bound != "" && c.operationID == bound
	var stop stoppedResources
	if matched {
		if c.updateShutdown != nil {
			c.updateShutdown.reconnect = false
		}
		stop = c.captureStopLocked()
	}
	c.mu.Unlock()
	if notify != nil {
		notify(targets)
	}
	if !matched {
		return nil
	}
	return c.waitStopped(stop)
}

func (c *Controller) CancelOperation(id string) error { return c.Cancel("", id) }

// An action is registered under the cancellation lock only after old helper
// cleanup. Cancellation either rejects its admission or joins its real finish;
// callbacks and logout subprocesses run without holding the controller lock.
type operationAction struct {
	done chan struct{}
}

func (c *Controller) admitOperationActionLocked(operation string) (*operationAction, error) {
	if c.closed || c.operationID != operation || c.operationAllowedLocked(operation) != nil {
		return nil, ErrOperationCanceled
	}
	if c.action != nil {
		return nil, tailsplit.ErrPingBusy
	}
	action := &operationAction{done: make(chan struct{})}
	c.action = action
	return action, nil
}

func (c *Controller) finishOperationActionLocked(action *operationAction) {
	if action == nil {
		return
	}
	if c.action == action {
		c.action = nil
	}
	close(action.done)
}

type stoppedResources struct {
	command                                     *exec.Cmd
	input                                       io.WriteCloser
	done, pingDone, preparationDone, actionDone <-chan struct{}
	generation                                  uint64
	completion                                  *stopCompletion
}

// All retries joining the same captured cleanup share one completion watcher.
// It is armed even if signaling fails, and survives individual caller deadlines.
type stopCompletion struct {
	resources stoppedResources
	done      chan struct{}
	once      sync.Once
}

func (c *Controller) captureStopLocked() stoppedResources {
	stop := stoppedResources{command: c.command, input: c.input, done: c.done, pingDone: c.pingDone}
	if c.command == nil {
		stop.done = nil
	}
	if c.action != nil {
		stop.actionDone = c.action.done
	}
	c.stopping = true
	if c.pingCancel != nil {
		c.pingCancel()
	}
	if c.preparation != nil {
		c.preparation.cancel()
		stop.preparationDone = c.preparation.done
		c.snapshot.State = "stopping"
	}
	if c.command != nil {
		c.snapshot.State = "stopping"
		c.snapshot.LoginURL = ""
	}
	if previous := c.stopCompletion; previous != nil {
		resources := previous.resources
		if resources.command == stop.command && resources.done == stop.done && resources.pingDone == stop.pingDone && resources.preparationDone == stop.preparationDone && resources.actionDone == stop.actionDone {
			select {
			case <-previous.done:
			default:
				stop.generation, stop.completion = resources.generation, previous
				return stop
			}
		}
	}
	c.stopGeneration++
	stop.generation = c.stopGeneration
	stop.completion = &stopCompletion{resources: stop, done: make(chan struct{})}
	c.stopCompletion = stop.completion
	return stop
}

func (c *Controller) waitStopped(stop stoppedResources) error {
	stop.completion.once.Do(func() {
		go func() {
			if stop.done != nil {
				<-stop.done
			}
			if stop.pingDone != nil {
				<-stop.pingDone
			}
			if stop.preparationDone != nil {
				<-stop.preparationDone
			}
			if stop.actionDone != nil {
				<-stop.actionDone
			}
			c.mu.Lock()
			if c.stopGeneration == stop.generation && c.stopCompletion == stop.completion {
				c.stopping = false
			}
			close(stop.completion.done)
			c.mu.Unlock()
		}()
	})
	if stop.command != nil {
		if err := requestProcessStop(stop.command, stop.input); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
	}
	timer := time.NewTimer(c.stopWait)
	defer timer.Stop()
	select {
	case <-stop.completion.done:
		return nil
	case <-timer.C:
		if stop.pingDone != nil {
			select {
			case <-stop.pingDone:
			default:
				return tailsplit.ErrPingBusy
			}
		}
		return errors.New("network service cleanup is still in progress")
	}
}
