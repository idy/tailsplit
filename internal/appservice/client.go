package appservice

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/idy/tailsplit"
)

var ErrNotInstalled = errors.New("the TailSplit network service is not installed")

// Launch brokers the app's network session without elevating the GUI.
func Launch(ctx context.Context, executable string, config tailsplit.Config, output io.Writer) error {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		return errors.New("app-run is for the macOS app; use run on other platforms")
	}
	connection, err := OpenSession(ctx, executable, "start", config, output)
	if err != nil {
		if ctx.Err() == nil {
			_ = json.NewEncoder(output).Encode(ErrorEvent(err))
		}
		return err
	}
	defer connection.Close()
	return Forward(ctx, connection, output)
}

func probe(ctx context.Context, path string, expectedUID string) (Info, error) {
	connection, err := (&net.Dialer{}).DialContext(ctx, "unix", path)
	if err != nil {
		return Info{}, ErrNotInstalled
	}
	defer connection.Close()
	if err := requireUser(connection, expectedUID); err != nil {
		return Info{}, err
	}
	_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
	if err := json.NewEncoder(connection).Encode(Request{Protocol: Protocol, Op: "hello"}); err != nil {
		return Info{}, err
	}
	var info Info
	if err := json.NewDecoder(io.LimitReader(connection, 4096)).Decode(&info); err != nil {
		return Info{}, err
	}
	if info.Event != "service" || info.Protocol != Protocol {
		return Info{}, errors.New("the app and installed service versions differ")
	}
	return info, nil
}

func packagePath(executable, name string) string {
	return filepath.Join(filepath.Dir(executable), "..", "Resources", name)
}

func serviceInstallPackage(executable, installed string, probeErr error) (string, error) {
	path := packagePath(executable, InstallPackage)
	if _, err := os.Lstat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) && errors.Is(probeErr, ErrNotInstalled) {
			if _, err := os.Lstat(installed); errors.Is(err, os.ErrNotExist) {
				// Source builds can authenticate locally only without an existing service.
				return "", ErrNotInstalled
			}
		}
		return "", errors.New("open the packaged TailSplit.app to install its network service")
	}
	return path, nil
}

// EnsureInstalled opens the normal macOS Installer when the helper is absent,
// differs from this app, or is no longer reachable through launchd. A matching
// file alone does not mean an interrupted installation registered its service.
// No Terminal, saved password or sudoers rule is used.
func EnsureInstalled(ctx context.Context, executable string, output io.Writer) (Info, error) {
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}
	expected, err := executableID(executable)
	if err != nil {
		return Info{}, err
	}
	info, err := probe(ctx, SocketPath, "0")
	if ctx.Err() != nil {
		return Info{}, ctx.Err()
	}
	if err == nil && info.BuildID == expected {
		return info, nil
	}
	path, err := serviceInstallPackage(executable, ExecutablePath, err)
	if err != nil {
		return Info{}, err
	}
	if err := json.NewEncoder(output).Encode(map[string]string{"event": "authorizing"}); err != nil {
		return Info{}, err
	}
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}
	if err := exec.Command("/usr/bin/open", "-a", "Installer", path).Run(); err != nil {
		return Info{}, errors.New("could not open the macOS network service installer")
	}
	installCtx, cancel := context.WithTimeout(ctx, 8*time.Minute)
	defer cancel()
	ticker := time.NewTicker(750 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-installCtx.Done():
			if err := ctx.Err(); err != nil {
				return Info{}, err
			}
			return Info{}, errors.New("network service installation did not finish; complete Installer, then retry in TailSplit")
		case <-ticker.C:
			info, err := probe(installCtx, SocketPath, "0")
			if err == nil && info.BuildID == expected {
				return info, nil
			}
		}
	}
}

// OpenSession transfers policy and eligible per-device migration candidates,
// holding the ordinary-user state lease through privileged session cleanup.
// It never transfers path authority.
func OpenSession(ctx context.Context, executable, operation string, config tailsplit.Config, output io.Writer) (net.Conn, error) {
	return openSession(ctx, executable, operation, config, output, "")
}

func openSession(ctx context.Context, executable, operation string, config tailsplit.Config, output io.Writer, target string) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if runtime.GOOS == "windows" {
		connection, err := openWindowsSession(ctx, executable, operation, config, target)
		if err != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return connection, windowsServiceFailure(operation, err)
	}
	info, err := resolveSessionService(ctx, operation, func(ctx context.Context) (Info, error) {
		info, err := probe(ctx, SocketPath, "0")
		if err != nil {
			if _, installedErr := os.Stat(ExecutablePath); installedErr == nil {
				return Info{}, errors.New("后台服务没有响应；请从“设置与工具 → 后台网络服务 → 更新或修复…”恢复服务")
			}
		}
		return info, err
	}, func(ctx context.Context) (Info, error) {
		return EnsureInstalled(ctx, executable, output)
	})
	if err != nil {
		return nil, installationFailure(operation, err)
	}
	expected, err := executableID(executable)
	if err != nil {
		return nil, err
	}
	if info.BuildID != expected {
		return nil, installationFailure(operation, errors.New("update the TailSplit network service from this app before continuing"))
	}
	if operation == "ping" && !info.PingAvailable {
		return nil, tailsplit.ErrPingUnavailable
	}
	return openLeasedSession(ctx, operation, config, target, info, func(ctx context.Context) (net.Conn, error) {
		connection, err := (&net.Dialer{}).DialContext(ctx, "unix", SocketPath)
		if err != nil {
			return nil, err
		}
		if err := requireUser(connection, "0"); err != nil {
			_ = connection.Close()
			return nil, err
		}
		return connection, nil
	})
}

// Only an explicit Connect or device sign-in may open Installer. Discovery,
// Ping and logout remain non-installing, including automatic startup probes.
// Preparing before the session/lease boundary also lets a first Add exit work
// without an existing service or a configured traffic gateway.
func resolveSessionService(ctx context.Context, operation string, probeService, ensureService func(context.Context) (Info, error)) (Info, error) {
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}
	resolve := probeService
	switch operation {
	case "start", "login", "login-new":
		resolve = ensureService
	}
	info, err := resolve(ctx)
	if err == nil {
		err = ctx.Err()
	}
	return info, err
}

// migrationIDs scopes ordinary-user reads to engines this operation can open.
// The complete configuration still crosses the boundary and retains its limit.
func migrationIDs(config tailsplit.Config, operation, target string) ([]string, error) {
	if err := validateExitInventory(len(config.Exits)); err != nil {
		return nil, err
	}
	if err := validateImportIDs(config.ExitIDs()); err != nil {
		return nil, err
	}
	focus := config.Focus
	if operation == "login-new" && !tailsplit.ValidExitID(focus) {
		return nil, errors.New("new device authentication requires a configured additional exit")
	}
	if operation == "ping" {
		if exit, _ := tailsplit.SplitPingTarget(target); exit != "" {
			focus = exit
		}
		if focus == "" {
			focus = tailsplit.MainExit
		}
		if _, err := config.FocusExit(focus); err != nil {
			return nil, err
		}
	} else if operation == "logout" && focus == "" {
		focus = tailsplit.MainExit
	} else if operation == "start" && config.Rules.Mode == tailsplit.RoutingGlobal && focus == "" {
		focus = config.TrafficExitID()
	}
	if focus != "" {
		for _, id := range config.ExitIDs() {
			if id == focus {
				if operation == "login-new" {
					return nil, nil // A fresh device never imports any identity or cache.
				}
				return []string{id}, nil
			}
		}
		return nil, errors.New("the selected migration exit is not configured")
	}
	if operation != "start" {
		return config.ExitIDs(), nil
	}
	var ids []string
	if config.Tailscale != nil && config.Tailscale.ExitNode != "" {
		ids = append(ids, tailsplit.MainExit)
	}
	for _, exit := range config.Exits {
		if exit.ExitNode != "" {
			ids = append(ids, exit.ID)
		}
	}
	return ids, nil
}

type sessionDial func(context.Context) (net.Conn, error)

func queryImports(ctx context.Context, ids []string, dial sessionDial) (ImportPlan, error) {
	connection, err := dial(ctx)
	if err != nil {
		return ImportPlan{}, err
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
	if err := json.NewEncoder(connection).Encode(Request{Protocol: Protocol, Op: "imports", ImportIDs: ids}); err != nil {
		return ImportPlan{}, err
	}
	var reply importReply
	if err := json.NewDecoder(io.LimitReader(connection, 4096)).Decode(&reply); err != nil {
		return ImportPlan{}, err
	}
	// Old services cannot silently turn a missing capability into an empty
	// migration plan. Build matching is also checked by each platform client.
	if reply.Event != "imports" || reply.Protocol != Protocol {
		return ImportPlan{}, errors.New("update the network service before migrating saved devices")
	}
	if err := validateImportIDs(reply.Plan.IDs); err != nil {
		return ImportPlan{}, err
	}
	requested := make(map[string]bool, len(ids))
	for _, id := range ids {
		requested[id] = true
	}
	for _, id := range reply.Plan.IDs {
		if !requested[id] {
			return ImportPlan{}, errors.New("the network service returned an unrelated migration identity")
		}
	}
	return reply.Plan, nil
}

// Both authenticated transports use this path. In particular, no legacy file
// may be opened before taking the same state lease as the ordinary-user CLI.
func openLeasedSession(ctx context.Context, operation string, config tailsplit.Config, target string, info Info, dial sessionDial) (net.Conn, error) {
	ids, err := migrationIDs(config, operation, target)
	if err != nil {
		return nil, err
	}
	unlock, err := tailsplit.LockSavedState(config.StateDir)
	activeOnly := false
	if err != nil {
		if operation != "ping" || !info.Initialized {
			return nil, err
		}
		activeOnly = true
		unlock = func() error { return nil }
	}
	keep := false
	defer func() {
		if !keep {
			_ = unlock()
		}
	}()
	if operation == "login-new" {
		// Claim the ordinary-user namespace too, under its lease. This rejects
		// existing CLI/fallback identities and blocks a later fallback or import
		// from reusing an unpublished device if the service attempt fails.
		if err := tailsplit.ReserveNewExitState(config); err != nil {
			return nil, err
		}
		// Device-only authentication never reads or transfers routing inputs.
		config.Rules.GFWList, config.Rules.Source = "", nil
	}
	if operation == "start" {
		if err := tailsplit.EnsureRules(ctx, config, nil); err != nil {
			return nil, err
		}
	}
	var files Files
	if !activeOnly && operation != "login-new" {
		plan, err := queryImports(ctx, ids, dial)
		if err != nil {
			return nil, err
		}
		files, err = userFiles(&config, plan)
		if err != nil {
			return nil, fmt.Errorf("read saved app state for migration: %w", err)
		}
	}
	// Active-only pings send no migration files and cannot start an idle engine.
	// Never ask the service to resolve any of the user's filesystem paths.
	config.StateDir = ""
	connection, err := dial(ctx)
	if err != nil {
		return nil, err
	}
	// No operation is sent until the service has registered this channel. A
	// failed handshake therefore cannot open a migrated identity.
	_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
	if err := json.NewEncoder(connection).Encode(Request{Protocol: Protocol, Op: "session"}); err != nil {
		_ = connection.Close()
		return nil, err
	}
	var session sessionReply
	if err := json.NewDecoder(io.LimitReader(connection, 4096)).Decode(&session); err != nil {
		_ = connection.Close()
		return nil, err
	}
	if session.Event != "session" || session.Protocol != Protocol || session.SessionID == "" || len(session.SessionID) > 128 {
		_ = connection.Close()
		return nil, errors.New("update the network service before opening a saved-device session")
	}
	_ = connection.SetDeadline(time.Time{})
	leased := &stateLeaseConnection{Conn: connection, release: unlock, sessionID: session.SessionID, dial: dial}
	keep = true
	_ = leased.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := json.NewEncoder(leased).Encode(Request{Protocol: Protocol, Op: operation, Target: target, ActiveOnly: activeOnly, Config: &config, Files: files}); err != nil {
		// Even a failed write may have delivered a complete request. Recovery
		// must join this registered session before releasing the state lease.
		leased.writeFailed = true
		if closeErr := leased.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
		return nil, err
	}
	_ = leased.SetWriteDeadline(time.Time{})
	return leased, nil
}

// recoverSession cancels and joins the registered original channel through the
// same authenticated endpoint. A service generation owns its private-state lease
// until all handlers finish, so an unknown ID also confirms cleanup after restart.
// Per-attempt deadlines only bound retries; they never release the user's lease.
func recoverSession(dial sessionDial, id string) {
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		connection, err := dial(ctx)
		cleaned := false
		if err == nil {
			_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
			err = json.NewEncoder(connection).Encode(Request{Protocol: Protocol, Op: "cleanup", SessionID: id})
			if err == nil {
				var reply sessionReply
				err = json.NewDecoder(io.LimitReader(connection, 4096)).Decode(&reply)
				cleaned = err == nil && reply.Event == "cleanup" && reply.Protocol == Protocol && reply.SessionID == id && reply.Cleaned
			}
			_ = connection.Close()
		}
		cancel()
		if cleaned {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// stopSession uses the existing lifetime command without closing the read side:
// the service sends its terminal event and EOF only after its runner returns.
func stopSession(connection net.Conn) {
	if leased, ok := connection.(*stateLeaseConnection); ok {
		leased.stopOnce.Do(func() {
			leased.stopErr = sendStop(leased.Conn)
			if leased.stopErr != nil {
				_ = leased.Conn.Close() // Unblock a pending reader; recover via its session ID.
			}
		})
		return
	}
	_ = sendStop(connection)
}

func sendStop(connection net.Conn) error {
	// An operation deadline may cancel its work, but it cannot certify cleanup.
	if err := connection.SetReadDeadline(time.Time{}); err != nil {
		return err
	}
	if err := connection.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		return err
	}
	_, err := io.WriteString(connection, "stop\n")
	return err
}

type stateLeaseConnection struct {
	net.Conn
	release     func() error
	sessionID   string
	dial        sessionDial
	writeFailed bool // Set before this connection is returned to callers.
	stopOnce    sync.Once
	stopErr     error
	readMu      sync.Mutex
	readErr     error
	readEOF     atomic.Bool
	once        sync.Once
	err         error
}

func (c *stateLeaseConnection) Read(data []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	n, err := c.Conn.Read(data)
	if errors.Is(err, io.EOF) {
		c.readEOF.Store(true)
	}
	if err != nil && !errors.Is(err, io.EOF) && !readTimedOut(err) {
		// Some broken transports return EOF on their next read. Remember
		// the failure so Close cannot mistake that for orderly cleanup.
		c.readErr = err
	}
	return n, err
}

func readTimedOut(err error) bool {
	var timeout net.Error
	return errors.As(err, &timeout) && timeout.Timeout()
}

func (c *stateLeaseConnection) Close() error {
	c.once.Do(func() {
		// Stop before taking readMu: an existing reader can be waiting for the
		// runner, which in turn needs this command to begin cleanup.
		confirmedEOF := c.readEOF.Load()
		if !confirmedEOF {
			stopSession(c)
		}
		c.readMu.Lock()
		// The joined reader may have received orderly EOF while stop failed.
		// Earlier hard failures remain poisoned through readErr/writeFailed.
		confirmedEOF = c.readEOF.Load()
		var err error
		if !confirmedEOF && c.readErr == nil && !c.writeFailed && c.stopErr == nil {
			for {
				_, err = io.Copy(io.Discard, c.Conn)
				if !readTimedOut(err) || c.Conn.SetReadDeadline(time.Time{}) != nil {
					break
				}
			}
		}
		err = errors.Join(c.readErr, err)
		closeErr := c.Conn.Close()
		if errors.Is(closeErr, net.ErrClosed) {
			closeErr = nil // A failed stop may already have closed the channel.
		}
		c.readMu.Unlock()
		if err != nil || c.writeFailed || !confirmedEOF && c.stopErr != nil {
			if err != nil || c.writeFailed {
				_, _ = fmt.Fprintln(os.Stderr, "Network service channel failed; confirming cleanup while saved device state remains locked.")
			}
			recoverSession(c.dial, c.sessionID)
		}
		c.err = errors.Join(closeErr, c.release())
	})
	return c.err
}

// Forward keeps the app channel alive until privileged cleanup completes.
// Cancellation abandons ordinary output and gives stopped a bounded delivery
// window while draining to service EOF. A caller-owned write can outlive Forward.
func Forward(ctx context.Context, connection net.Conn, output io.Writer) error {
	stop := sync.OnceFunc(func() { stopSession(connection) })
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			stop()
		case <-done:
		}
	}()
	defer close(done)
	writes, written := make(chan []byte), make(chan error, 1)
	// One worker owns output and copied bytes only. An arbitrary caller-owned
	// Writer may remain blocked after cancellation, without holding the lease.
	go func() {
		for data := range writes {
			n, err := output.Write(data)
			if err == nil && n != len(data) {
				err = io.ErrShortWrite
			}
			written <- err
		}
	}()
	defer close(writes)
	outputCanceled, writePending := false, false
	writeLine := func(line []byte, terminal bool) error {
		if ctx.Err() != nil && !terminal {
			outputCanceled = true
			return nil
		}
		if writePending {
			// Never queue the terminal event behind an already blocked writer.
			select {
			case err := <-written:
				writePending = false
				if err != nil {
					return err
				}
			default:
				return nil
			}
		}
		writeCtx := ctx
		var cancelGrace context.CancelFunc
		defer func() {
			if cancelGrace != nil {
				cancelGrace()
			}
		}()
		abandon := func() bool {
			if terminal && cancelGrace == nil {
				// Only stopped gets output grace. Service cleanup has no deadline.
				writeCtx, cancelGrace = context.WithTimeout(context.Background(), 100*time.Millisecond)
				return false
			}
			outputCanceled = true
			return true
		}
		data := append(append([]byte(nil), line...), '\n')
	submit:
		for {
			select {
			case writes <- data:
				writePending = true
				break submit
			case <-writeCtx.Done():
				if abandon() {
					return nil
				}
			}
		}
		for {
			select {
			case err := <-written:
				writePending = false
				return err
			case <-writeCtx.Done():
				// Prefer a completed write's real error over simultaneous cancellation.
				select {
				case err := <-written:
					writePending = false
					return err
				default:
				}
				if abandon() {
					return nil
				}
			}
		}
	}
	scanner := bufio.NewScanner(connection)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	stopped, terminalTried := false, false
	var writeErr error
	for scanner.Scan() {
		line := scanner.Bytes()
		var event struct {
			Event string `json:"event"`
		}
		terminal := json.Unmarshal(line, &event) == nil && event.Event == "stopped"
		stopped = stopped || terminal
		forward := !outputCanceled
		if terminal && ctx.Err() != nil {
			forward = !terminalTried
		}
		if writeErr == nil && forward {
			terminalTried = terminalTried || terminal
			if err := writeLine(line, terminal); err != nil {
				writeErr = err
				stop()
			}
		}
	}
	if writeErr == nil {
		// A canceled output may have completed while service cleanup was draining.
		select {
		case writeErr = <-written:
		default:
		}
	}
	if writeErr != nil {
		if err := scanner.Err(); err != nil {
			return errors.Join(writeErr, err)
		}
		return writeErr
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if ctx.Err() != nil || stopped {
		return nil
	}
	return errors.New("the network service session ended; see its status in the app")
}

func Logout(ctx context.Context, executable string, config tailsplit.Config) error {
	connection, err := OpenSession(ctx, executable, "logout", config, io.Discard)
	if err != nil {
		return err
	}
	defer connection.Close()
	deadline, ok := ctx.Deadline()
	if ok {
		_ = connection.SetDeadline(deadline)
	}
	reader := bufio.NewScanner(connection)
	reader.Buffer(make([]byte, 4096), 1<<20)
	for reader.Scan() {
		var event struct {
			Event   string `json:"event"`
			Message string `json:"message"`
		}
		if json.Unmarshal(reader.Bytes(), &event) != nil {
			continue
		}
		if event.Event == "error" {
			return errors.New(event.Message)
		}
		if event.Event == "stopped" {
			return nil
		}
	}
	return errors.New("network service device logout did not finish")
}
