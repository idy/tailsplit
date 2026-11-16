//go:build !windows

package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"time"

	"github.com/idy/tailsplit/internal/appservice"
)

// Installer runs as root, but the service admits only its registered desktop
// user. Re-exec the installed helper with that user's credentials for the same
// authenticated hello used by the app. This never opens a network engine.
func checkServiceCommand(args []string, diagnostic io.Writer) error {
	if runtime.GOOS != "darwin" {
		return errors.New("service-check requires macOS")
	}
	flags := flag.NewFlagSet("service-check", flag.ContinueOnError)
	flags.SetOutput(diagnostic)
	uid := flags.Int("uid", -1, "registered desktop user")
	gid := flags.Int("gid", -1, "registered desktop group")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected service-check argument")
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if *uid != -1 || *gid != -1 {
		if os.Geteuid() != 0 || *uid < 500 || uint64(*uid) > uint64(^uint32(0)) || *gid < 0 || uint64(*gid) > uint64(^uint32(0)) {
			return errors.New("service-check credential selection requires root and a desktop UID/GID")
		}
		command := exec.CommandContext(ctx, executable, "service-check")
		command.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(*uid), Gid: uint32(*gid)}}
		command.Stdout, command.Stderr = diagnostic, diagnostic
		return command.Run()
	}
	return appservice.CheckInstalled(ctx, executable)
}
