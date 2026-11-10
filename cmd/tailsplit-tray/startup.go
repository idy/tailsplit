package main

import (
	"context"
	"os"
	"path/filepath"
)

func configureDesktopProof(directory string) error {
	if directory == "" {
		return nil
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return err
	}
	return os.Setenv("TAILSPLIT_DESKTOP_PROOF_DIR", absolute)
}

func desktopProofMode() bool { return os.Getenv("TAILSPLIT_DESKTOP_PROOF_DIR") != "" }

// Service readiness precedes login, discovery or connection. A restart with no
// connection action still prepares its already-installed service, while errors
// or cancellation must never start a helper against an outdated service.
func prepareDesktopStartup(ctx context.Context, prepare func(context.Context) error, start func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := prepare(ctx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if start != nil {
		return start()
	}
	return nil
}
