package tray

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/pingenv"
)

func (c *Controller) pingAvailableLocked() bool {
	if c.closed || c.stopping || c.loggingOut || c.reconfiguring || !c.snapshot.PingAvailable || c.snapshot.BackendState != "Running" {
		return false
	}
	if c.command == nil {
		return true
	}
	// The native desktops use the service, whose active engine accepts ping
	// requests. A standalone non-service CLI engine has no control channel.
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		return false
	}
	switch c.snapshot.State {
	case "running", "blocked", "missing-exit", "waiting-exit":
		return c.snapshot.Mode == "proxy" || c.snapshot.Mode == "tun"
	default:
		return false
	}
}

// PingExitNode is independent of Start/Stop and never reconnects the proxy.
// The helper contacts the installed service's active engine or, when idle,
// opens a scoped session for that same saved device. Its output is bounded.
func (c *Controller) PingExitNode(ctx context.Context, id string) (tailsplit.ExitPingResult, error) {
	c.mu.Lock()
	if c.pingCancel != nil || c.reconfiguring {
		c.mu.Unlock()
		return tailsplit.ExitPingResult{}, tailsplit.ErrPingBusy
	}
	if !c.pingAvailableLocked() {
		c.mu.Unlock()
		return tailsplit.ExitPingResult{}, tailsplit.ErrPingUnavailable
	}
	if err := tailsplit.ValidateExitPingTarget(c.snapshot.ExitNodes, id); err != nil {
		c.mu.Unlock()
		return tailsplit.ExitPingResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, tailsplit.ExitPingTimeout)
	defer cancel()
	done := make(chan struct{})
	c.pingCancel, c.pingDone = cancel, done
	c.mu.Unlock()
	type response struct {
		result tailsplit.ExitPingResult
		err    error
	}
	responseCh := make(chan response, 1)
	go func() {
		result, err := c.runPingHelper(ctx, id)
		c.mu.Lock()
		c.pingCancel, c.pingDone = nil, nil
		close(done)
		c.mu.Unlock()
		responseCh <- response{result: result, err: err}
	}()
	select {
	case reply := <-responseCh:
		return reply.result, reply.err
	case <-ctx.Done():
		// The caller may stop waiting, but the helper retains the device lease
		// and keeps new operations blocked until service cleanup completes.
		return tailsplit.ExitPingResult{}, ctx.Err()
	}
}

func (c *Controller) runPingHelper(ctx context.Context, id string) (tailsplit.ExitPingResult, error) {
	operation := "ping"
	if len(c.options.HelperArgs) == 0 && (runtime.GOOS == "darwin" || runtime.GOOS == "windows") {
		operation = "app-ping"
	}
	args := append(append([]string{}, c.options.HelperArgs...), operation, "--id", id, "--parent-pid", fmt.Sprint(os.Getpid()), "-c", c.options.ConfigPath)
	command := exec.Command(c.options.Helper, args...)
	command.Dir = filepath.Dir(c.options.ConfigPath)
	command.Env = pingenv.WithoutAuthentication(append(os.Environ(), c.options.ExtraEnv...))
	command.Stderr = c.log
	lease, err := prepareProcess(command, true)
	if err != nil {
		return tailsplit.ExitPingResult{}, tailsplit.ErrPingUnavailable
	}
	if lease != nil {
		defer lease.Close()
	}
	var output bytes.Buffer
	command.Stdout = &limitedPingWriter{buffer: &output, remaining: 4096}
	if err := ctx.Err(); err != nil {
		return tailsplit.ExitPingResult{}, err
	}
	if err := command.Start(); err != nil {
		return tailsplit.ExitPingResult{}, tailsplit.ErrPingUnavailable
	}
	// Let the helper forward cancellation and wait for service cleanup before
	// it exits and releases its Windows saved-state lease.
	stop := context.AfterFunc(ctx, func() { _ = requestProcessStop(command, lease) })
	err = command.Wait()
	stop()
	if ctx.Err() != nil {
		return tailsplit.ExitPingResult{}, ctx.Err()
	}
	var reply tailsplit.ExitPingReply
	if json.Unmarshal(output.Bytes(), &reply) != nil {
		return tailsplit.ExitPingResult{}, tailsplit.ErrPingFailed
	}
	if reply.Error != "" {
		return tailsplit.ExitPingResult{}, tailsplit.PingError(reply.Error)
	}
	if err != nil || reply.Result == nil || reply.Result.ID != id {
		return tailsplit.ExitPingResult{}, tailsplit.ErrPingFailed
	}
	return *reply.Result, nil
}

type limitedPingWriter struct {
	buffer    *bytes.Buffer
	remaining int
}

func (w *limitedPingWriter) Write(data []byte) (int, error) {
	if len(data) > w.remaining {
		return 0, io.ErrShortBuffer
	}
	w.remaining -= len(data)
	return w.buffer.Write(data)
}
