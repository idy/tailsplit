package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"runtime"

	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/appservice"
)

func runPingCommand(ctx context.Context, app bool, config tailsplit.Config, id string, output io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, tailsplit.ExitPingTimeout)
	defer cancel()
	var result tailsplit.ExitPingResult
	var err error
	if id == "" || len(id) > 256 {
		err = tailsplit.ErrPingTarget
	} else if app {
		var executable string
		executable, err = os.Executable()
		if err == nil {
			result, err = appservice.PingExitNode(ctx, executable, config, id)
			if errors.Is(err, appservice.ErrNotInstalled) && runtime.GOOS == "darwin" {
				// This is the same fallback used by device discovery/login before
				// the network service is installed. Never install it just to ping.
				result, err = tailsplit.PingSavedExitNode(ctx, config, id)
			}
		}
	} else {
		result, err = tailsplit.PingSavedExitNode(ctx, config, id)
	}
	reply := tailsplit.ExitPingReply{Result: &result}
	if err != nil {
		reply = tailsplit.ExitPingReply{Error: tailsplit.PingErrorCode(err)}
	}
	// The private controller consumes the error code even when the operation
	// fails. Raw service/core errors never enter this protocol or browser API.
	return json.NewEncoder(output).Encode(reply)
}
