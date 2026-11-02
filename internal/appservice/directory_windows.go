//go:build windows

package appservice

import "github.com/idy/tailsplit/internal/privatefs"

// The SCM service runs as SYSTEM: only SYSTEM and administrators can modify
// these directories. The desktop user receives data over the pipe, never paths.
func privateDirectory(path string) error { return privatefs.EnsureDir(path) }
