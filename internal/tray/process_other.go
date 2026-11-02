//go:build !windows

package tray

import (
	"io"
	"os/exec"
	"syscall"
)

func prepareProcess(*exec.Cmd, bool) (io.WriteCloser, error) { return nil, nil }
func requestProcessStop(command *exec.Cmd, _ io.WriteCloser) error {
	return command.Process.Signal(syscall.SIGTERM)
}
