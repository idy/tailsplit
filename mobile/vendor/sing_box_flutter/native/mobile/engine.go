// SPDX-License-Identifier: GPL-3.0-or-later
package libbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/filemanager"
	"github.com/sagernet/sing/service/pause"

	"golang.org/x/sys/unix"
)

type boxEngine struct {
	box       *box.Box
	ctx       context.Context
	pause     pause.Manager
	log       *os.File
	closeOnce sync.Once
	closeErr  error
}

func newBoxEngine(ctx context.Context, platform PlatformInterface, options option.Options) (mobileEngine, error) {
	ctx = pause.WithDefaultManager(ctx)
	ctx = service.ContextWith[adapter.PlatformInterface](ctx, &platformInterfaceWrapper{iif: platform})
	path, err := privateLogPath(ctx, options.Log)
	if err != nil {
		return nil, err
	}
	engine := &boxEngine{ctx: ctx, pause: service.FromContext[pause.Manager](ctx)}
	if path != "" {
		engine.log, err = openPrivateLog(ctx, path)
		if err != nil {
			_ = engine.Close()
			return nil, errors.New("could not open private mobile log")
		}
	}
	// Upstream protocol logs may contain credential-bearing URLs. An explicit
	// log target receives only fixed lifecycle events, never those diagnostics.
	options.Log = &option.LogOptions{Disabled: true}
	// The pinned mobile constructor always enabled cache via PlatformLogWriter.
	// Preserve that default without restoring raw logs or the RPC layer.
	var experimental option.ExperimentalOptions
	if options.Experimental != nil {
		experimental = *options.Experimental
	}
	var cache option.CacheFileOptions
	if experimental.CacheFile != nil {
		cache = *experimental.CacheFile
	}
	cache.Enabled = true
	experimental.CacheFile = &cache
	options.Experimental = &experimental
	engine.box, err = box.New(box.Options{Context: ctx, Options: options})
	if err != nil {
		engine.event("create failed")
		_ = engine.Close()
		return nil, errors.New("could not create mobile service")
	}
	return engine, nil
}

func privateLogPath(ctx context.Context, options *option.LogOptions) (string, error) {
	if options == nil || options.Disabled {
		return "", nil
	}
	if options.Level != "" {
		if _, err := log.ParseLevel(options.Level); err != nil {
			return "", errors.New("invalid mobile log level")
		}
	}
	output := options.Output
	if output == "stdout" || output == "stderr" {
		return "", errors.New("mobile logging requires a private file")
	}
	if output == "" {
		output = "mobile.log"
	}
	path := filepath.Clean(filemanager.BasePath(ctx, output))
	root, relative, err := privateLogLocation(ctx, path)
	if err != nil {
		return "", err
	}
	// Validation is read-only. Opening repeats this check using directory file
	// descriptors so a symlink swap between validation and startup cannot escape.
	current := root
	parts := strings.Split(relative, string(filepath.Separator))
	for index, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			break
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 || (index < len(parts)-1 && !info.IsDir()) || (index == len(parts)-1 && !info.Mode().IsRegular()) {
			return "", errors.New("mobile log path must be a private regular file")
		}
	}
	return path, nil
}

func privateLogLocation(ctx context.Context, path string) (string, string, error) {
	// The host supplies the private working root via filemanager, never config.
	root := filepath.Clean(filemanager.BasePath(ctx, "."))
	relative, err := filepath.Rel(root, path)
	if !filepath.IsAbs(root) || err != nil || !filepath.IsLocal(relative) || relative == "." {
		return "", "", errors.New("mobile log path must stay inside private working storage")
	}
	return root, relative, nil
}

func openPrivateLog(ctx context.Context, path string) (*os.File, error) {
	root, relative, err := privateLogLocation(ctx, path)
	if err != nil {
		return nil, err
	}
	// The host root may have system aliases (e.g. /var on Apple). Below that
	// trusted root, no component may be a symlink, even one pointing back inside.
	directory, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = unix.Close(directory) }()
	parts := strings.Split(relative, string(filepath.Separator))
	for _, part := range parts[:len(parts)-1] {
		next, err := unix.Openat(directory, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if errors.Is(err, unix.ENOENT) {
			if err = unix.Mkdirat(directory, part, 0o700); err != nil && !errors.Is(err, unix.EEXIST) {
				return nil, err
			}
			next, err = unix.Openat(directory, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		}
		if err != nil {
			return nil, err
		}
		_ = unix.Close(directory)
		directory = next
	}
	fd, err := unix.Openat(directory, parts[len(parts)-1], unix.O_CREAT|unix.O_APPEND|unix.O_WRONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	var stat unix.Stat_t
	if err = unix.Fstat(fd, &stat); err == nil && (stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1) {
		err = errors.New("mobile log must be a private regular file")
	}
	if err == nil {
		err = file.Chmod(0o600)
	}
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func (e *boxEngine) event(event string) {
	if e.log != nil {
		_, _ = e.log.WriteString(time.Now().UTC().Format(time.RFC3339) + " " + event + "\n")
	}
}

func (e *boxEngine) Start() error {
	err := e.box.Start()
	if err != nil {
		e.event("start failed")
	} else {
		e.event("started")
	}
	return err
}

func (e *boxEngine) Close() error {
	e.closeOnce.Do(func() {
		if e.box != nil {
			e.closeErr = e.box.Close()
		}
		closeHistory(e.ctx)
		e.event("stopped")
		if e.log != nil {
			if err := e.log.Close(); e.closeErr == nil {
				e.closeErr = err
			}
			e.log = nil
		}
	})
	return e.closeErr
}

func closeHistory(ctx context.Context) {
	if history := service.PtrFromContext[urltest.HistoryStorage](ctx); history != nil {
		history.Close()
	}
}

func (e *boxEngine) Pause() { e.box.CloseIdleConnections(); e.pause.DevicePause(); e.event("paused") }

// Upstream ignores Wake on iOS and ends the pause from WakeNow/screen-state
// reports. This binding has neither path: hosts call only Pause and Wake, and
// Pause is unconditional, so Wake must end the device pause on every platform.
// DeviceWake is a no-op when the device is not paused.
func (e *boxEngine) Wake()               { e.pause.DeviceWake(); e.event("awake") }
func (e *boxEngine) NeedWIFIState() bool { return e.box.Network().NeedWIFIState() }
