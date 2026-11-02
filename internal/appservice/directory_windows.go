//go:build windows

package appservice

import "errors"

func privateDirectory(string) error {
	return errors.New("the installed app service is available only on macOS")
}
