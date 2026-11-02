//go:build !windows

package appservice

import (
	"context"
	"errors"
	"net"

	"github.com/idy/tailsplit"
)

func openWindowsSession(context.Context, string, string, tailsplit.Config) (net.Conn, error) {
	return nil, errors.New("Windows network sessions require Windows")
}
