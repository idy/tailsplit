//go:build !windows

package appservice

import (
	"errors"
	"net"

	"github.com/tailscale/peercred"
)

func validServiceUser(uid int, user string) bool { return uid > 0 && user == "" }

func requireUser(connection net.Conn, expected string) error {
	credentials, err := peercred.Get(connection)
	if err != nil {
		return errors.New("could not authenticate the local network service")
	}
	uid, ok := credentials.UserID()
	if !ok || uid != expected {
		return errors.New("the network service channel has an unexpected user")
	}
	return nil
}
