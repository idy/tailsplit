//go:build windows

package appservice

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"testing"
	"time"
	"unsafe"

	"github.com/tailscale/go-winio"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

func TestWindowsMigratedExitRetainsPrivateRootACL(t *testing.T) {
	base := filepath.Join(t.TempDir(), "service")
	files := Files{ExitStates: map[string][]byte{"office": []byte("cli-identity")}}
	directory, err := prepareRuntimeUser(base, "fixture-user", files)
	if err != nil {
		t.Fatal(err)
	}
	// A directory made with os.MkdirTemp alone inherits an unprotected ACL;
	// after publication it must satisfy the same root checks as other exits.
	if err := privateDirectory(filepath.Join(directory, "exits", "office")); err != nil {
		t.Fatal("migration published an exit without private root authority:", err)
	}
	if _, err := prepareRuntimeUser(base, "fixture-user", files); err != nil {
		t.Fatal("published migration could not be reused:", err)
	}
	assertMigrationFile(t, filepath.Join(directory, "exits", "office", "tailscale", "tailscaled.state"), "cli-identity")
}

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

func TestWindowsServicePeerRequiresMatchingProtectedSCMIdentity(t *testing.T) {
	installed := `C:\Program Files\TailSplit\tailsplit-core.exe`
	const pid = uint32(4321)
	trusted := mgr.Config{ServiceType: windows.SERVICE_WIN32_OWN_PROCESS,
		ServiceStartName: "LocalSystem", BinaryPathName: windows.EscapeArg(installed) + " service"}
	running := svc.Status{State: svc.Running, ProcessId: pid}
	if err := verifyServiceProcess(pid, installed, trusted, running); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*uint32, *mgr.Config, *svc.Status)
	}{
		{"different pipe process", func(p *uint32, _ *mgr.Config, _ *svc.Status) { *p = pid + 1 }},
		{"missing process", func(p *uint32, _ *mgr.Config, _ *svc.Status) { *p = 0 }},
		{"stopping service", func(_ *uint32, _ *mgr.Config, s *svc.Status) { s.State = svc.StopPending }},
		{"restarted service", func(_ *uint32, _ *mgr.Config, s *svc.Status) { s.ProcessId++ }},
		{"ordinary account", func(_ *uint32, c *mgr.Config, _ *svc.Status) { c.ServiceStartName = `.\desktop-user` }},
		{"network service account", func(_ *uint32, c *mgr.Config, _ *svc.Status) { c.ServiceStartName = `NT AUTHORITY\NetworkService` }},
		{"shared service host", func(_ *uint32, c *mgr.Config, _ *svc.Status) { c.ServiceType = windows.SERVICE_WIN32_SHARE_PROCESS }},
		{"caller-owned core", func(_ *uint32, c *mgr.Config, _ *svc.Status) {
			c.BinaryPathName = `C:\Users\Public\tailsplit-core.exe service`
		}},
		{"extra service argument", func(_ *uint32, c *mgr.Config, _ *svc.Status) { c.BinaryPathName += " --user-sid S-1-5-18" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			p, config, status := pid, trusted, running
			test.change(&p, &config, &status)
			if verifyServiceProcess(p, installed, config, status) == nil {
				t.Fatal("accepted an unrelated or untrusted service endpoint")
			}
		})
	}
}

// processACEs lists each SID's process ACEs as sorted "type/flags/mask" text.
func processACEs(t *testing.T, process windows.Handle) map[string][]string {
	t.Helper()
	descriptor, err := windows.GetSecurityInfo(process, windows.SE_KERNEL_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		t.Fatal("cannot read the process DACL:", err)
	}
	entries := make(map[string][]string)
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &ace); err != nil {
			t.Fatal(err)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String()
		entries[sid] = append(entries[sid], fmt.Sprintf("%d/%d/%#x", ace.Header.AceType, ace.Header.AceFlags, ace.Mask))
	}
	for _, list := range entries {
		slices.Sort(list)
	}
	return entries
}

func TestWindowsServiceProcessGrantsOnlyIdentityQuery(t *testing.T) {
	// A canonical desktop SID that is not this test account. The grant is
	// applied to this test process, whose original DACL is restored afterwards.
	const user = "S-1-5-21-1-2-3-1001"
	process := windows.CurrentProcess()
	original, err := windows.GetSecurityInfo(process, windows.SE_KERNEL_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := original.DACL()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = windows.SetSecurityInfo(process, windows.SE_KERNEL_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
	})
	before := processACEs(t, process)
	if before[user] != nil {
		t.Fatal("the fixture SID already has process access")
	}
	if allowIdentityQuery(process, "../users") == nil {
		t.Fatal("accepted an invalid desktop SID")
	}
	if err := allowIdentityQuery(process, user); err != nil {
		t.Fatal(err)
	}
	after := processACEs(t, process)
	want := []string{fmt.Sprintf("%d/0/%#x", windows.ACCESS_ALLOWED_ACE_TYPE, windows.PROCESS_QUERY_LIMITED_INFORMATION)}
	if !slices.Equal(after[user], want) {
		t.Fatalf("registered user process access = %v, want only %v", after[user], want)
	}
	delete(after, user)
	if !maps.EqualFunc(before, after, slices.Equal[[]string]) {
		t.Fatalf("the grant changed other process access: before %v, after %v", before, after)
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
