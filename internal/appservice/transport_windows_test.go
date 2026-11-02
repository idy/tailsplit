//go:build windows

package appservice

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/tailscale/go-winio"
	"golang.org/x/sys/windows"
)

func TestWindowsRegisteredServiceUser(t *testing.T) {
	for _, test := range []struct {
		user  string
		valid bool
	}{
		{"S-1-5-21-1-2-3-1001", true},
		{"S-1-12-1-1-2-3-4", true},
		{systemSID, false}, {"S-1-5-32-544", false}, {"../users", false}, {"", false},
	} {
		if validServiceUser(0, test.user) != test.valid {
			t.Fatalf("unexpected registered SID policy for %q", test.user)
		}
	}
	if validServiceUser(501, "S-1-5-21-1-2-3-1001") {
		t.Fatal("accepted a mixed Unix/Windows identity")
	}
}

func TestWindowsPipeDoesNotTrustAnUninstalledUserServer(t *testing.T) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	name := `\\.\pipe\tailsplit-untrusted-` + filepath.Base(t.TempDir())
	listener, err := winio.ListenPipe(name, &winio.PipeConfig{SecurityDescriptor: "D:P(A;;GA;;;" + user.User.Sid.String() + ")"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		connection, err := listener.Accept()
		if err == nil {
			defer connection.Close()
			var data [1]byte
			_, _ = connection.Read(data[:])
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	connection, err := winio.DialPipeAccess(ctx, name, 0x12019b)
	if err != nil {
		t.Fatal(err)
	}
	if requireUser(connection, systemSID) == nil {
		t.Fatal("trusted a user pipe as the SYSTEM service")
	}
	connection.Close()
	<-done
}
