package main

import (
	"context"
	"errors"
	"sync"

	"github.com/idy/tailsplit/internal/appupdate"
	"github.com/idy/tailsplit/internal/configweb"
	"github.com/idy/tailsplit/internal/tray"
)

// Update work has its own lifetime; downloading never holds the network
// controller's operation queue. Only preference writes and installation enter it.
type desktopUpdateSource interface {
	Snapshot() appupdate.State
	SetAutomatic(bool)
	Check(context.Context) error
	Download(context.Context) (appupdate.Package, error)
}

type desktopUpdates struct {
	updater desktopUpdateSource
	ctx     context.Context
	save    func(context.Context, bool) error
	install func(appupdate.Package) error
	confirm func(string) bool

	mu         sync.Mutex
	busy       bool
	installing bool
	err        string
}

func newDesktopUpdates(ctx context.Context, version string, automatic bool, save func(context.Context, bool) error, install func(appupdate.Package) error) *desktopUpdates {
	updater := appupdate.New(version, automatic)
	d := &desktopUpdates{ctx: ctx, updater: updater, save: save, install: install, confirm: tray.ConfirmUpdate}
	go updater.Run(ctx)
	return d
}

func (d *desktopUpdates) snapshot() appupdate.State {
	d.mu.Lock()
	defer d.mu.Unlock()
	state := d.updater.Snapshot()
	state.Installing = d.installing
	if d.err != "" {
		state.Error = d.err
	}
	return state
}

func (d *desktopUpdates) action(ctx context.Context, action string) error {
	if err := d.ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	if d.busy {
		d.mu.Unlock()
		return configweb.ErrBusy
	}
	switch action {
	case "automatic-on", "automatic-off":
		d.busy = true
		d.mu.Unlock()
		enabled := action == "automatic-on"
		err := d.save(ctx, enabled)
		if err == nil {
			d.updater.SetAutomatic(enabled)
		}
		d.mu.Lock()
		d.busy = false
		d.mu.Unlock()
		return err
	case "check", "install":
		if action == "install" && !d.updater.Snapshot().Available {
			d.mu.Unlock()
			return errors.New("请先检查并选择可用的新版本")
		}
		d.busy = true
		d.installing = action == "install"
		d.err = ""
		d.mu.Unlock()
		go func() {
			var err error
			if action == "check" {
				err = d.updater.Check(d.ctx)
			} else {
				confirmedVersion := d.updater.Snapshot().LatestVersion
				if d.confirm(confirmedVersion) {
					var update appupdate.Package
					update, err = d.updater.Download(d.ctx)
					if err == nil && update.Version != confirmedVersion {
						_ = update.Cleanup()
						err = errors.New("可用版本已变化，请重新确认升级")
					}
					if err == nil {
						err = d.install(update)
						if err != nil {
							_ = update.Cleanup()
						}
					}
				}
			}
			d.mu.Lock()
			if err != nil && d.ctx.Err() == nil {
				d.err = err.Error()
			}
			d.busy, d.installing = false, false
			d.mu.Unlock()
		}()
		return nil
	default:
		d.mu.Unlock()
		return errors.New("unknown update action")
	}
}

func updateMenuTitleForLanguage(state appupdate.State, language string) string {
	if state.Available && !state.Downloading && !state.Installing && !state.Checking {
		return tray.Translate(language, "升级到 ") + state.LatestVersion + "…"
	}
	return tray.Translate(language, updateMenuTitle(state))
}

func updateMenuTitle(state appupdate.State) string {
	switch {
	case state.Downloading:
		return "正在下载更新…"
	case state.Installing:
		return "正在准备升级…"
	case state.Checking:
		return "正在检查更新…"
	case state.Available:
		return "升级到 " + state.LatestVersion + "…"
	case state.Error != "":
		return "检查更新失败，重试…"
	case !state.LastChecked.IsZero():
		return "已是最新版本 · 检查更新…"
	default:
		return "检查更新…"
	}
}
