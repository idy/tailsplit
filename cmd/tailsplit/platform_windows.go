//go:build windows

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/appservice"
)

func platformCommand(args []string, out, diagnostic io.Writer) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	switch args[0] {
	case "windows-install-service":
		flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
		flags.SetOutput(diagnostic)
		user := flags.String("user-sid", "", "originating desktop user SID for silent deployment")
		if err := flags.Parse(args[1:]); err != nil {
			return true, err
		}
		if flags.NArg() != 0 {
			return true, errors.New("unexpected service installation argument")
		}
		return true, appservice.InstallWindowsService(*user)
	case "windows-stop-service", "windows-remove-service":
		if len(args) != 1 {
			return true, errors.New("unexpected service maintenance argument")
		}
		return true, appservice.ManageWindowsService(args[0] == "windows-remove-service")
	case "windows-service-status":
		if len(args) != 1 {
			return true, errors.New("unexpected service status argument")
		}
		status, err := appservice.WindowsServiceStatus()
		if err == nil {
			_, err = fmt.Fprintln(out, status)
		}
		return true, err
	}
	return false, nil
}

func platformInstalledService(args []string) (bool, error) {
	if len(args) != 0 {
		return true, errors.New("the Windows SCM service accepts no user-supplied arguments")
	}
	return true, appservice.RunWindowsInstalled(
		func(ctx context.Context, config tailsplit.Config, output io.Writer) error {
			compiled, err := tailsplit.CompileContext(ctx, config)
			if err != nil {
				return err
			}
			stream := newEventStream(ctx, output, true)
			defer stream.close()
			return runEvents(ctx, config, compiled, stream)
		},
		func(ctx context.Context, config tailsplit.Config, output io.Writer) error {
			stream := newEventStream(ctx, output, true)
			defer stream.close()
			return runLoginEvents(ctx, config, stream)
		},
		func(ctx context.Context, config tailsplit.Config, _ io.Writer) error {
			return tailsplit.LogoutSavedTailnet(ctx, config)
		},
		func(ctx context.Context, config tailsplit.Config, output io.Writer) error {
			stream := newEventStream(ctx, output, true)
			defer stream.close()
			return runDiscoveryEvents(ctx, config, stream)
		},
	)
}

func prepareAppProcess() {}

// The GUI owns the sole write end. Stop or GUI death closes the lease, even
// though a Windows parent PID does not change when its process exits.
func watchAppParent(_ context.Context, _ int, cancel func()) {
	var command [1]byte
	_, _ = os.Stdin.Read(command[:])
	cancel()
}
