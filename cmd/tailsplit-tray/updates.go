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

type desktopUpdatePhase uint8

const (
	desktopUpdateIdle desktopUpdatePhase = iota
	desktopUpdateSaving
	desktopUpdateChecking
	desktopUpdateConfirming
	desktopUpdateDownloading
	desktopUpdateInstalling
)

type desktopUpdates struct {
	updater desktopUpdateSource
	ctx     context.Context
	save    func(context.Context, bool) error
	install func(appupdate.Package) error
	confirm func(string) bool

	mu    sync.Mutex
	phase desktopUpdatePhase
	err   string
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
	// Publish queued work before its goroutine reaches the updater, without
	// hiding checks or downloads started by the updater's automatic schedule.
	state.Checking = state.Checking || d.phase == desktopUpdateChecking
	state.Downloading = state.Downloading || d.phase == desktopUpdateDownloading
	state.Busy = d.phase != desktopUpdateIdle || state.Checking || state.Downloading
	state.Confirming = d.phase == desktopUpdateConfirming
	state.Installing = d.phase == desktopUpdateInstalling
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
	if d.phase != desktopUpdateIdle {
		d.mu.Unlock()
		return configweb.ErrBusy
	}
	switch action {
	case "automatic-on", "automatic-off":
		d.phase = desktopUpdateSaving
		d.mu.Unlock()
		enabled := action == "automatic-on"
		err := d.save(ctx, enabled)
		if err == nil {
			d.updater.SetAutomatic(enabled)
		}
		d.setPhase(desktopUpdateIdle)
		return err
	case "check", "install":
		if action == "install" && !d.updater.Snapshot().Available {
			d.mu.Unlock()
			return errors.New("请先检查并选择可用的新版本")
		}
		d.phase = desktopUpdateChecking
		d.err = ""
		d.mu.Unlock()
		go func() {
			err := d.run(action)
			d.mu.Lock()
			if err != nil && d.ctx.Err() == nil {
				d.err = err.Error()
			}
			d.phase = desktopUpdateIdle
			d.mu.Unlock()
		}()
		return nil
	default:
		d.mu.Unlock()
		return errors.New("unknown update action")
	}
}

func (d *desktopUpdates) setPhase(phase desktopUpdatePhase) {
	d.mu.Lock()
	d.phase = phase
	d.mu.Unlock()
}

func (d *desktopUpdates) run(action string) error {
	// An offer can be hours old. Refresh before confirmation so one
	// installation jumps straight to the current stable release, while
	// Download still pins the exact version the user confirmed.
	if err := d.updater.Check(d.ctx); err != nil {
		return err
	}
	if action != "install" {
		return nil
	}
	latest := d.updater.Snapshot()
	if !latest.Available {
		return nil
	}
	if err := d.ctx.Err(); err != nil {
		return err
	}
	confirmedVersion := latest.LatestVersion
	d.setPhase(desktopUpdateConfirming)
	if !d.confirm(confirmedVersion) {
		return nil
	}
	if err := d.ctx.Err(); err != nil {
		return err
	}
	d.setPhase(desktopUpdateDownloading)
	update, err := d.updater.Download(d.ctx)
	if err != nil {
		return err
	}
	if update.Version != confirmedVersion {
		_ = update.Cleanup()
		return errors.New("可用版本已变化，请重新确认升级")
	}
	if err = d.ctx.Err(); err == nil {
		d.setPhase(desktopUpdateInstalling)
		err = d.install(update)
	}
	if err != nil {
		_ = update.Cleanup()
	}
	return err
}
