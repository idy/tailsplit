//go:build windows

package appservice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"github.com/tailscale/go-winio"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const windowsRegistration = `SOFTWARE\TailSplit`

func registeredWindowsUser() (string, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, windowsRegistration, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return "", err
	}
	defer key.Close()
	user, _, err := key.GetStringValue("DesktopUserSID")
	if err != nil || !validServiceUser(0, user) {
		return "", errors.New("the Windows network service has no valid registered desktop user")
	}
	return user, nil
}

// Use the shell's token in this interactive session, not the account whose
// credentials approved UAC. Silent deployment supplies an explicit SID instead.
func InteractiveWindowsUser() (string, error) {
	window, _, _ := windows.NewLazySystemDLL("user32.dll").NewProc("GetShellWindow").Call()
	if window == 0 {
		return "", errors.New("no interactive desktop user; silent installation needs /UserSID=<desktop user SID>")
	}
	var pid uint32
	windows.NewLazySystemDLL("user32.dll").NewProc("GetWindowThreadProcessId").Call(window, uintptr(unsafe.Pointer(&pid)))
	user, image, err := processIdentity(pid)
	if err != nil || !strings.EqualFold(filepath.Base(image), "explorer.exe") || !validServiceUser(0, user) {
		return "", errors.New("could not identify the originating Windows desktop user")
	}
	return user, nil
}

func requireWindowsInstaller() (string, error) {
	if !windows.GetCurrentProcessToken().IsElevated() {
		return "", errors.New("Windows service installation requires administrator approval")
	}
	installed, err := windowsCorePath()
	if err != nil {
		return "", err
	}
	executable, err := os.Executable()
	if err != nil || !strings.EqualFold(executable, installed) {
		return "", errors.New("install the network service only from its protected Program Files location")
	}
	return installed, protectedWindowsImage(installed)
}

// InstallWindowsService changes SCM/registration only. It never removes user
// data or connects an engine. The installer owns file installation and rollback.
func InstallWindowsService(user string) error {
	installed, err := requireWindowsInstaller()
	if err != nil {
		return err
	}
	if user == "" {
		user, err = InteractiveWindowsUser()
		if err != nil {
			return err
		}
	}
	if !validServiceUser(0, user) {
		return errors.New("expected a canonical desktop account SID")
	}
	previous, registeredErr := registeredWindowsUser()
	if registeredErr == nil && previous != user {
		return errors.New("TailSplit is registered to another desktop user; repair it from that user's desktop")
	}
	manager, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer manager.Disconnect()
	service, err := manager.OpenService(windowsServiceName)
	// CreateService supplies a default service type; UpdateConfig does not.
	// Use the same complete SCM policy for first install and later repairs.
	config := mgr.Config{
		DisplayName: "TailSplit Network Service",
		Description: "Runs the TailSplit network session requested by its registered desktop user.",
		ServiceType: windows.SERVICE_WIN32_OWN_PROCESS, ErrorControl: mgr.ErrorNormal,
		StartType: mgr.StartAutomatic, DelayedAutoStart: true,
	}
	if err == nil {
		defer service.Close()
		if err := stopWindowsService(service); err != nil {
			return err
		}
		config.BinaryPathName = windows.EscapeArg(installed) + " service"
		if err := service.UpdateConfig(config); err != nil {
			return fmt.Errorf("update Windows network service configuration: %w", err)
		}
	} else if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		service, err = manager.CreateService(windowsServiceName, installed, config, "service")
		if err != nil {
			return err
		}
		defer service.Close()
	} else {
		return err
	}
	key, _, err := registry.CreateKey(registry.LOCAL_MACHINE, windowsRegistration, registry.SET_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return err
	}
	err = key.SetStringValue("DesktopUserSID", user)
	key.Close()
	if err != nil {
		return err
	}
	if err := service.Start(); err != nil && !errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		status, err := service.Query()
		if err != nil {
			return err
		}
		if status.State == svc.Running {
			return nil
		}
		if status.State == svc.Stopped {
			return errors.New("the Windows network service stopped during installation; installation was not completed")
		}
		select {
		case <-ctx.Done():
			return errors.New("the Windows network service did not start in time")
		case <-ticker.C:
		}
	}
}

func stopWindowsService(service *mgr.Service) error {
	status, err := service.Query()
	if err != nil || status.State == svc.Stopped {
		return err
	}
	if status.State != svc.StopPending {
		if _, err := service.Control(svc.Stop); err != nil && !errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
			return err
		}
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		status, err := service.Query()
		if err != nil {
			return err
		}
		if status.State == svc.Stopped {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("the network service is still cleaning up; the installer will not replace running files")
}

// ManageWindowsService is called only by the elevated installer before replacing
// files or uninstalling. User configuration and service identity are retained.
func ManageWindowsService(remove bool) error {
	if _, err := requireWindowsInstaller(); err != nil {
		return err
	}
	manager, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer manager.Disconnect()
	service, err := manager.OpenService(windowsServiceName)
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return nil
	}
	if err != nil {
		return err
	}
	defer service.Close()
	if err := stopWindowsService(service); err != nil {
		return err
	}
	if remove {
		return service.Delete()
	}
	return nil
}

func RunWindowsInstalled(run, login, logout, discover Runner) error {
	user, err := registeredWindowsUser()
	if err != nil {
		return err
	}
	installed, err := windowsCorePath()
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	identity, errToken := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || errToken != nil || !strings.EqualFold(executable, installed) || identity.User.Sid.String() != systemSID {
		return errors.New("the installed Windows network service must run as SYSTEM from Program Files")
	}
	if err := protectedWindowsImage(installed); err != nil {
		return err
	}
	buildID, err := executableID(installed)
	if err != nil {
		return err
	}
	base, err := windowsStatePath()
	if err != nil {
		return err
	}
	if err := privateDirectory(base); err != nil {
		return err
	}
	server := &Server{UserID: user, Base: base, BuildID: buildID, Run: run, Login: login, Logout: logout, Discover: discover}
	return svc.Run(windowsServiceName, &windowsService{server: server})
}

type windowsService struct{ server *Server }

func (s *windowsService) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending, WaitHint: 10000}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// go-winio rejects remote clients and creates the first pipe instance
	// exclusively, so another process cannot become a competing server.
	listener, err := winio.ListenPipe(windowsPipe, &winio.PipeConfig{SecurityDescriptor: "O:SYD:P(A;;GA;;;SY)(A;;0x12019b;;;" + s.server.UserID + ")", InputBufferSize: 65536, OutputBufferSize: 65536})
	if err != nil {
		return true, 1
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() { done <- s.server.Serve(ctx, listener) }()
	current := svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown | svc.AcceptPreShutdown}
	status <- current
	for {
		select {
		case err := <-done:
			if err != nil {
				return true, 2
			}
			return false, 0
		case request, ok := <-requests:
			if !ok {
				cancel()
				<-done
				return false, 0
			}
			switch request.Cmd {
			case svc.Interrogate:
				status <- current
			case svc.Stop, svc.Shutdown, svc.PreShutdown:
				cancel()
				for checkpoint := uint32(1); ; checkpoint++ {
					status <- svc.Status{State: svc.StopPending, WaitHint: 10000, CheckPoint: checkpoint}
					select {
					case err := <-done:
						if err != nil {
							return true, 3
						}
						return false, 0
					case <-time.After(time.Second):
					}
				}
			}
		}
	}
}

func WindowsServiceStatus() (string, error) {
	manager, err := mgr.Connect()
	if err != nil {
		return "", err
	}
	defer manager.Disconnect()
	service, err := manager.OpenService(windowsServiceName)
	if err != nil {
		return "", err
	}
	defer service.Close()
	status, err := service.Query()
	return fmt.Sprint(status.State), err
}
