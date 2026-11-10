//go:build windows

package main

import (
	"bytes"
	"context"
	"errors"
	"image/png"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/idy/tailsplit/internal/configweb"
	"github.com/idy/tailsplit/internal/tray"
	"github.com/tailscale/go-winio"
	"golang.org/x/sys/windows"
)

func TestWindowsOperationsRejectEditsWhileLoginBrowserOpens(t *testing.T) {
	directory := t.TempDir()
	config := filepath.Join(directory, "config.yaml")
	if err := initializeWindowsConfig(config); err != nil {
		t.Fatal(err)
	}
	controller, err := tray.New(tray.Options{
		Helper: filepath.Join(directory, "unused.exe"), ConfigPath: config,
		LogPath: filepath.Join(directory, "tailsplit.log"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	app := &windowsApp{ctx: ctx, controller: controller, operations: make(chan windowsOperation, 1), browserDone: make(chan error)}
	app.login.Request()
	if app.login.Next(tray.Snapshot{LoginURL: "https://login.tailscale.com/a/test"}) == "" {
		cancel()
		t.Fatal("login browser was not marked as opening")
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		app.operate()
	}()
	defer func() { cancel(); <-done }()
	applied := false
	if err := app.do(ctx, func() error { applied = true; return nil }); !errors.Is(err, configweb.ErrBusy) || applied {
		t.Fatal("edit ran while the old browser request was opening:", applied, err)
	}
	select {
	case app.browserDone <- nil:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	failure := errors.New("operation failed")
	if err := app.do(ctx, func() error { return failure }); !errors.Is(err, failure) {
		t.Fatal("operation did not resume or return its failure after browser completion:", err)
	}
}

func TestWindowsDesktopPipeOwnerCanAcceptClients(t *testing.T) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	name := `\\.\pipe\tailsplit-desktop-owner-` + filepath.Base(t.TempDir())
	listener, err := winio.ListenPipe(name, &winio.PipeConfig{SecurityDescriptor: desktopPipeDescriptor(user.User.Sid.String())})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if err == nil {
			defer connection.Close()
			_, err = io.WriteString(connection, "ok\n")
		}
		done <- err
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	connection, err := winio.DialPipeAccess(ctx, name, 0x12019b)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_ = connection.SetReadDeadline(time.Now().Add(3 * time.Second))
	var reply [3]byte
	if _, err := io.ReadFull(connection, reply[:]); err != nil {
		t.Fatal(err)
	}
	if string(reply[:]) != "ok\n" {
		t.Fatal("unexpected GUI control response")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestWindowsTrayIconPreservesSelectedArtwork(t *testing.T) {
	for _, connected := range []bool{false, true} {
		data := windowsTrayIcon(connected)
		picture, err := png.Decode(bytes.NewReader(data))
		if err != nil || picture.Bounds().Dx() != 64 || picture.Bounds().Dy() != 64 {
			t.Fatal("invalid tray PNG:", err)
		}
		var expected bytes.Buffer
		if err := png.Encode(&expected, markImage(64, true, connected)); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(data, expected.Bytes()) {
			t.Fatal("tray artwork changed")
		}
	}
}
