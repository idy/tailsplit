package appservice

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/idy/tailsplit"
)

type Runner func(context.Context, tailsplit.Config, io.Writer) error

type Server struct {
	UID      int
	Base     string
	BuildID  string
	Run      Runner
	Login    Runner
	Logout   Runner
	Discover Runner
	active   sync.Mutex
	requests sync.WaitGroup
}

// Serve accepts only the user approved during package installation. Socket
// permissions and peer credentials are independent checks. Only one network
// engine can run; losing its app channel cancels it and waits for cleanup.
func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	if s.UID <= 0 || s.Base == "" || s.BuildID == "" || s.Run == nil || s.Logout == nil {
		return errors.New("invalid network service setup")
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = listener.Close()
		case <-done:
		}
	}()
	defer close(done)
	defer s.requests.Wait()
	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		s.requests.Add(1)
		go func() {
			defer s.requests.Done()
			defer connection.Close()
			s.handle(ctx, connection)
		}()
	}
}

func (s *Server) handle(ctx context.Context, connection net.Conn) {
	if requireUser(connection, strconv.Itoa(s.UID)) != nil {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream := &eventWriter{connection: connection, cancel: cancel}
	fail := func(message string) {
		_ = stream.send(map[string]string{"event": "error", "message": message})
	}
	_ = connection.SetReadDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReader(connection)
	request, err := readRequest(reader)
	if err != nil {
		fail(err.Error())
		return
	}
	_ = connection.SetReadDeadline(time.Time{})
	if request.Op == "hello" {
		_ = stream.send(Info{Event: "service", Protocol: Protocol, Version: Version,
			BuildID: s.BuildID, Initialized: initialized(s.Base, s.UID)})
		return
	}
	if request.Op != "start" && request.Op != "login" && request.Op != "logout" && request.Op != "discover" || request.Config == nil {
		fail("the network service supports only status, start, discovery and device login/logout")
		return
	}
	if !s.active.TryLock() {
		fail("another TailSplit app is using the network service; stop it first")
		return
	}
	defer s.active.Unlock()
	config, err := serviceConfig(*request.Config, runtimeDirectory(s.Base, s.UID))
	if err != nil {
		fail(err.Error())
		return
	}
	if request.Op == "login" || request.Op == "discover" {
		config.TUN.Enabled = false
	}
	if _, err = prepareRuntime(s.Base, s.UID, request.Files); err != nil {
		fail("could not prepare private network service state")
		return
	}
	// Start the lifetime monitor before opening any core. Both explicit stop
	// and a vanished app close the engine, DNS session and routes.
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		var command [1]byte
		_, _ = reader.Read(command[:])
		cancel()
	}()
	defer func() {
		_ = connection.SetReadDeadline(time.Now())
		<-watched
	}()
	run := s.Run
	if request.Op == "login" {
		run = s.Login
		if run == nil {
			fail("this network service does not support separate device login; update it from the app")
			return
		}
	}
	if request.Op == "logout" {
		run = s.Logout
	}
	if request.Op == "discover" {
		run = s.Discover
		if run == nil {
			fail("update the network service before refreshing exit nodes")
			return
		}
	}
	if err := run(ctx, config, stream); err != nil && ctx.Err() == nil {
		fail(err.Error())
	} else {
		_ = stream.send(map[string]string{"event": "stopped", "mode": "tun"})
	}
}

// RunInstalled is called only by the root-owned binary installed by Installer.
// The listener itself is created and owned by launchd, not in a user directory.
func RunInstalled(ctx context.Context, uid int, run, login, logout, discover Runner) error {
	if os.Geteuid() != 0 || uid <= 0 {
		return errors.New("the installed network service requires root and its registered desktop user")
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	if executable != ExecutablePath {
		return errors.New("the network service must run from its installed, root-owned location")
	}
	buildID, err := executableID(executable)
	if err != nil {
		return err
	}
	listener, err := activatedListener()
	if err != nil {
		return err
	}
	defer listener.Close()
	server := &Server{UID: uid, Base: SupportPath, BuildID: buildID, Run: run, Login: login, Logout: logout, Discover: discover}
	return server.Serve(ctx, listener)
}
