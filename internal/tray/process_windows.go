//go:build windows

package tray

import (
	"io"
	"os/exec"
	"syscall"
)

func prepareProcess(command *exec.Cmd, lease bool) (io.WriteCloser, error) {
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if lease {
		return command.StdinPipe()
	}
	return nil, nil
}

func requestProcessStop(_ *exec.Cmd, input io.WriteCloser) error {
	if input == nil {
		return nil
	}
	_, _ = io.WriteString(input, "stop\n")
	_ = input.Close()
	// A closed/broken pipe already ended the lease. The controller must still
	// wait for process completion and its service cleanup acknowledgement.
	return nil
}
