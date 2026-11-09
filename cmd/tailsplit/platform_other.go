//go:build !windows

package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"
)

func platformCommand([]string, io.Writer, io.Writer) (bool, error) { return false, nil }
func platformInstalledService([]string) (bool, error)              { return false, nil }

// GUI exit closes stdout/stderr while service cleanup can still be running.
// Return write errors to the broker instead of terminating on a late SIGPIPE.
func prepareAppProcess() { signal.Ignore(syscall.SIGPIPE) }

func watchAppParent(ctx context.Context, pid int, cancel func()) {
	watchParent(ctx, pid, os.Getppid, cancel)
}
