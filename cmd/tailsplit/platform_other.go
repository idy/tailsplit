//go:build !windows

package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"
)

func platformCommand(args []string, _ io.Writer, diagnostic io.Writer) (bool, error) {
	if len(args) > 0 && args[0] == "service-check" {
		return true, checkServiceCommand(args[1:], diagnostic)
	}
	return false, nil
}

func platformInstalledService([]string) (bool, error) { return false, nil }

// GUI exit closes stdout/stderr while service cleanup can still be running.
// Return write errors to the broker instead of terminating on a late SIGPIPE.
func prepareAppProcess() { signal.Ignore(syscall.SIGPIPE) }

func watchAppParent(ctx context.Context, pid int, cancel func()) {
	watchParent(ctx, pid, os.Getppid, cancel)
}
