//go:build windows

package appservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"github.com/idy/tailsplit"
	"github.com/tailscale/go-winio"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const windowsServiceName = "TailSplitNetworkService"
const windowsPipe = `\\.\pipe\im.lbv.tailsplit.network.v1`
const systemSID = "S-1-5-18"

func validServiceUser(uid int, user string) bool {
	sid, err := windows.StringToSid(user)
	return uid == 0 && err == nil && sid.String() == user && (strings.HasPrefix(user, "S-1-5-21-") || strings.HasPrefix(user, "S-1-12-1-"))
}

func windowsCorePath() (string, error) {
	base, err := windows.KnownFolderPath(windows.FOLDERID_ProgramFiles, 0)
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "TailSplit", "tailsplit-core.exe"), nil
}

func windowsStatePath() (string, error) {
	base, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, 0)
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "TailSplit"), nil
}

func processIdentity(pid uint32) (string, string, error) {
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", "", err
	}
	defer windows.CloseHandle(process)
	var token windows.Token
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token); err != nil {
		return "", "", err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return "", "", err
	}
	image, err := processImage(process)
	if err != nil {
		return "", "", err
	}
	return user.User.Sid.String(), image, nil
}

func processImage(process windows.Handle) (string, error) {
	name := make([]uint16, 32768)
	length := uint32(len(name))
	if err := windows.QueryFullProcessImageName(process, 0, &name[0], &length); err != nil {
		return "", err
	}
	return windows.UTF16ToString(name[:length]), nil
}

// Ordinary users cannot reliably query a SYSTEM token. SCM independently
// binds the actual pipe PID to our running LocalSystem service and fixed image.
// These handles request only connect/query rights, never service administration.
// The service grants its registered user the limited process query used here.
func installedServiceProcess(pid uint32, installed string) (string, error) {
	manager, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return "", fmt.Errorf("read the local service manager: %w", err)
	}
	defer windows.CloseServiceHandle(manager)
	name, _ := windows.UTF16PtrFromString(windowsServiceName)
	handle, err := windows.OpenService(manager, name, windows.SERVICE_QUERY_STATUS|windows.SERVICE_QUERY_CONFIG)
	if err != nil {
		return "", fmt.Errorf("read the installed network service: %w", err)
	}
	service := &mgr.Service{Name: windowsServiceName, Handle: handle}
	defer service.Close()
	config, err := service.Config()
	if err != nil {
		return "", fmt.Errorf("read the network service identity: %w", err)
	}
	status, err := service.Query()
	if err != nil {
		return "", fmt.Errorf("read the network service process: %w", err)
	}
	if err := verifyServiceProcess(pid, installed, config, status); err != nil {
		return "", err
	}
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", fmt.Errorf("read the network service image: %w", err)
	}
	defer windows.CloseHandle(process)
	image, err := processImage(process)
	if err != nil {
		return "", fmt.Errorf("identify the network service image: %w", err)
	}
	// Reject a service restart while reading its image, rather than trusting a
	// stale status record or any PID/path claimed by the pipe peer itself.
	status, err = service.Query()
	if err != nil {
		return "", fmt.Errorf("confirm the network service process: %w", err)
	}
	if err := verifyServiceProcess(pid, installed, config, status); err != nil {
		return "", err
	}
	return image, nil
}

func verifyServiceProcess(pid uint32, installed string, config mgr.Config, status svc.Status) error {
	if pid == 0 || status.State != svc.Running || status.ProcessId != pid {
		return errors.New("the pipe endpoint is not the running TailSplit network service")
	}
	if config.ServiceType != windows.SERVICE_WIN32_OWN_PROCESS || !strings.EqualFold(config.ServiceStartName, "LocalSystem") {
		return errors.New("the installed network service does not have the required SYSTEM identity")
	}
	if !strings.EqualFold(config.BinaryPathName, windows.EscapeArg(installed)+" service") {
		return errors.New("the installed network service has an unexpected image or arguments")
	}
	return nil
}

// Authenticate the actual pipe endpoint, including its protected image path.
// A JSON SID, claimed PID, or merely matching executable filename is insufficient.
func requireUser(connection net.Conn, expected string) error {
	pipe, ok := connection.(interface{ Fd() uintptr })
	if !ok {
		return errors.New("the network service requires a local named pipe")
	}
	var pid uint32
	var err error
	if expected == systemSID {
		err = windows.GetNamedPipeServerProcessId(windows.Handle(pipe.Fd()), &pid)
	} else {
		err = windows.GetNamedPipeClientProcessId(windows.Handle(pipe.Fd()), &pid)
	}
	if err != nil {
		return errors.New("could not identify the local network service peer")
	}
	installed, err := windowsCorePath()
	if err != nil {
		return err
	}
	var image string
	if expected == systemSID {
		image, err = installedServiceProcess(pid, installed)
		if err != nil {
			return err
		}
	} else {
		var sid string
		sid, image, err = processIdentity(pid)
		if err != nil {
			return fmt.Errorf("read the desktop channel identity: %w", err)
		}
		if sid != expected {
			return errors.New("the network service channel has an unexpected Windows user")
		}
	}
	if !strings.EqualFold(filepath.Clean(image), installed) {
		return errors.New("the network service peer is not the installed TailSplit core")
	}
	if err := protectedWindowsImage(installed); err != nil {
		return err
	}
	return nil
}

// Read/execute grants are normal in Program Files. Reject write/delete/ACL
// authority for anyone other than SYSTEM, administrators or TrustedInstaller.
func protectedWindowsImage(path string) error {
	for _, name := range []string{filepath.Dir(filepath.Dir(path)), filepath.Dir(path), path} {
		info, err := os.Lstat(name)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("the installed network service path is unavailable or redirected")
		}
		wide, err := windows.UTF16PtrFromString(name)
		if err != nil {
			return err
		}
		handle, err := windows.CreateFile(wide, windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
			windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		if err != nil {
			return err
		}
		err = checkInstalledHandle(handle)
		windows.CloseHandle(handle)
		if err != nil {
			return err
		}
	}
	return nil
}

func checkInstalledHandle(handle windows.Handle) error {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("installed service paths must not be reparse points")
	}
	descriptor, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	trusted := func(sid *windows.SID) bool {
		return sid != nil && (sid.IsWellKnown(windows.WinLocalSystemSid) || sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) ||
			sid.String() == "S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464")
	}
	owner, _, err := descriptor.Owner()
	if err != nil || !trusted(owner) {
		return errors.New("the installed service must be owned by Windows or administrators")
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		return errors.New("the installed service needs a protected Windows access policy")
	}
	const writes = windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA | windows.FILE_WRITE_EA | windows.FILE_WRITE_ATTRIBUTES |
		0x40 /* FILE_DELETE_CHILD */ | windows.DELETE | windows.WRITE_DAC | windows.WRITE_OWNER | windows.GENERIC_WRITE | windows.GENERIC_ALL
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &ace); err != nil {
			return err
		}
		if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 || ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return errors.New("unsupported installed service access policy")
		}
		if ace.Mask&writes != 0 && !trusted((*windows.SID)(unsafe.Pointer(&ace.SidStart))) {
			return errors.New("the installed service is writable by an unprivileged account")
		}
	}
	return nil
}

func windowsDial(ctx context.Context) (net.Conn, error) {
	connection, err := winio.DialPipeAccess(ctx, windowsPipe, 0x12019b)
	if err != nil {
		return nil, ErrNotInstalled
	}
	if err := requireUser(connection, systemSID); err != nil {
		connection.Close()
		return nil, err
	}
	return connection, nil
}

func windowsProbe(ctx context.Context) (Info, error) {
	connection, err := windowsDial(ctx)
	if err != nil {
		return Info{}, err
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
	if err := json.NewEncoder(connection).Encode(Request{Protocol: Protocol, Op: "hello"}); err != nil {
		return Info{}, err
	}
	var info Info
	if err := json.NewDecoder(io.LimitReader(connection, 4096)).Decode(&info); err != nil {
		return Info{}, err
	}
	if info.Event != "service" || info.Protocol != Protocol {
		return Info{}, errors.New("the app and Windows network service protocols differ")
	}
	return info, nil
}

func openWindowsSession(ctx context.Context, executable, operation string, config tailsplit.Config, target string) (net.Conn, error) {
	installed, err := windowsCorePath()
	if err != nil || !strings.EqualFold(filepath.Clean(executable), installed) {
		return nil, errors.New("open the installed TailSplit desktop app")
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	info, err := windowsProbe(probeCtx)
	cancel()
	if err != nil {
		return nil, fmt.Errorf("Windows network service is unavailable; repair TailSplit with its installer: %w", err)
	}
	expected, err := executableID(executable)
	if err != nil {
		return nil, err
	}
	if info.BuildID != expected {
		return nil, errors.New("the desktop app and network service builds differ; complete the TailSplit upgrade")
	}
	if operation == "ping" && !info.PingAvailable {
		return nil, tailsplit.ErrPingUnavailable
	}
	return openLeasedSession(ctx, operation, config, target, info, windowsDial)
}
