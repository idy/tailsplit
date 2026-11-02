//go:build !windows

package main

import (
	"context"
	"io"
	"os"
)

func platformCommand([]string, io.Writer, io.Writer) (bool, error) { return false, nil }
func platformInstalledService([]string) (bool, error)              { return false, nil }
func watchAppParent(ctx context.Context, pid int, cancel func()) {
	watchParent(ctx, pid, os.Getppid, cancel)
}
