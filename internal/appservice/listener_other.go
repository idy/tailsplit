//go:build !darwin

package appservice

import (
	"errors"
	"net"
)

func activatedListener() (net.Listener, error) {
	return nil, errors.New("the installed app service is available only on macOS")
}
