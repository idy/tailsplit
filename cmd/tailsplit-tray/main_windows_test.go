//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"image/png"
	"io"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/tailscale/go-winio"
	"golang.org/x/sys/windows"
)

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

func TestWindowsTrayIconIncludesEachSystemSize(t *testing.T) {
	data := windowsTrayIcon()
	var header [3]uint16
	if err := binary.Read(bytes.NewReader(data), binary.LittleEndian, &header); err != nil || header[0] != 0 || header[1] != 1 {
		t.Fatal("invalid icon header:", header, err)
	}
	var sizes []int
	for i := 0; i < int(header[2]); i++ {
		entry := data[6+16*i : 6+16*(i+1)]
		length, offset := binary.LittleEndian.Uint32(entry[8:12]), binary.LittleEndian.Uint32(entry[12:16])
		picture, err := png.Decode(bytes.NewReader(data[offset : offset+length]))
		if err != nil || picture.Bounds().Dx() != int(entry[0]) || picture.Bounds().Dy() != int(entry[1]) {
			t.Fatalf("icon image %d does not match its directory entry: %v", i, err)
		}
		sizes = append(sizes, int(entry[0]))
	}
	if !slices.Equal(sizes, windowsTrayIconSizes) {
		t.Fatal("unexpected tray icon sizes:", sizes)
	}
}
