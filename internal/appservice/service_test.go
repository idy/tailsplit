package appservice

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/idy/tailsplit"
)

func TestStopWaitsForThePrivilegedCoreToFinish(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	broker, core := net.Pipe()
	defer broker.Close()
	defer core.Close()
	var output bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- Forward(ctx, broker, &output) }()
	cancel()
	var command [5]byte
	_ = core.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := io.ReadFull(core, command[:]); err != nil || string(command[:]) != "stop\n" {
		t.Fatalf("privileged core did not receive stop: %q, %v", command, err)
	}
	select {
	case <-done:
		t.Fatal("the broker exited before privileged route cleanup finished")
	default:
	}
	if _, err := io.WriteString(core, "{\"event\":\"stopped\"}\n"); err != nil {
		t.Fatal(err)
	}
	_ = core.Close()
	select {
	case err := <-done:
		if err != nil || output.String() != "{\"event\":\"stopped\"}\n" {
			t.Fatalf("lost shutdown status: %q, %v", output.String(), err)
		}
	case <-time.After(time.Second):
		t.Fatal("the broker did not finish after privileged cleanup")
	}
}

type observedReadConnection struct {
	net.Conn
	started    chan struct{}
	once       sync.Once
	mu         sync.Mutex
	reading    bool
	concurrent bool
}

func (c *observedReadConnection) Read(data []byte) (int, error) {
	c.mu.Lock()
	c.concurrent = c.concurrent || c.reading
	c.reading = true
	c.mu.Unlock()
	c.once.Do(func() { close(c.started) })
	n, err := c.Conn.Read(data)
	c.mu.Lock()
	c.reading = false
	c.mu.Unlock()
	return n, err
}

func TestStateLeaseCloseJoinsPendingReaderAndDuplicateClose(t *testing.T) {
	client, service := net.Pipe()
	defer service.Close()
	observed := &observedReadConnection{Conn: client, started: make(chan struct{})}
	released := make(chan struct{}, 2)
	releaseErr := errors.New("fixture unlock error")
	connection := &stateLeaseConnection{Conn: observed, release: func() error {
		released <- struct{}{}
		return releaseErr
	}}
	cleanup := make(chan struct{})
	finish := sync.OnceFunc(func() { close(cleanup) })
	defer finish()
	canceled := make(chan struct{})
	go func() {
		defer service.Close()
		var command [5]byte
		if _, err := io.ReadFull(service, command[:]); err != nil || string(command[:]) != "stop\n" {
			return
		}
		close(canceled)
		<-cleanup
		_, _ = io.WriteString(service, "{\"event\":\"stopped\"}\n")
	}()
	readerDone := make(chan error, 1)
	go func() { readerDone <- Forward(context.Background(), connection, io.Discard) }()
	select {
	case <-observed.started:
	case <-time.After(time.Second):
		t.Fatal("client did not begin reading")
	}
	closed := make(chan error, 2)
	go func() { closed <- connection.Close() }()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("Close deadlocked behind its pending reader")
	}
	go func() { closed <- connection.Close() }()
	select {
	case <-released:
		t.Fatal("Close released the lease before cleanup")
	case err := <-closed:
		t.Fatalf("Close returned before cleanup: %v", err)
	default:
	}
	finish()
	for range 2 {
		select {
		case err := <-closed:
			if !errors.Is(err, releaseErr) {
				t.Fatalf("Close lost its release error: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("Close did not finish after cleanup")
		}
	}
	select {
	case err := <-readerDone:
		if err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, io.ErrClosedPipe) {
			t.Fatal("pending reader did not finish cleanly", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close left its existing reader blocked")
	}
	observed.mu.Lock()
	concurrent := observed.concurrent
	observed.mu.Unlock()
	if concurrent {
		t.Fatal("Close and Forward read the service stream concurrently")
	}
	<-released
	select {
	case <-released:
		t.Fatal("duplicate Close released the lease twice")
	default:
	}
}

func TestStateLeaseCloseBoundsBrokenService(t *testing.T) {
	client, service := net.Pipe()
	defer service.Close()
	released := make(chan struct{})
	connection := &stateLeaseConnection{Conn: client, release: func() error { close(released); return nil }}
	closed := make(chan error, 1)
	// The peer neither reads stop nor acknowledges cleanup. Both directions
	// must be bounded, and duplicate Close must not restart that budget.
	go func() { closed <- connection.Close() }()
	select {
	case err := <-closed:
		var timeout net.Error
		if !errors.As(err, &timeout) || !timeout.Timeout() {
			t.Fatalf("unacknowledged cleanup did not report a timeout: %v", err)
		}
		if again := connection.Close(); again != err {
			t.Fatalf("duplicate Close changed its result: %v, %v", err, again)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("Close hung on an unresponsive service")
	}
	select {
	case <-released:
	default:
		t.Fatal("failed transport retained its local lease after the bounded wait")
	}
}

func privateTestDirectory(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	return directory
}

func testServer(t *testing.T, uid int, run Runner, discovery ...Runner) (string, *Server, context.CancelFunc, <-chan error) {
	t.Helper()
	directory, err := os.MkdirTemp("/tmp", "tailsplit-service-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	path := filepath.Join(directory, "control")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	discover := run
	if len(discovery) > 0 {
		discover = discovery[0]
	}
	server := &Server{UID: uid, Base: privateTestDirectory(t), BuildID: "test-build", Run: run, Login: run, Logout: run, Discover: discover}
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, listener) }()
	return path, server, cancel, done
}

func TestDiscoveryUsesSavedDeviceWithoutStartingTheConfiguredTUN(t *testing.T) {
	called := make(chan tailsplit.Config, 1)
	path, _, cancel, done := testServer(t, os.Getuid(), func(context.Context, tailsplit.Config, io.Writer) error {
		t.Error("discovery invoked the network startup runner")
		return nil
	}, func(_ context.Context, config tailsplit.Config, out io.Writer) error {
		called <- config
		return json.NewEncoder(out).Encode(map[string]string{"event": "discovered"})
	})
	connection, decoder := session(t, path, Request{
		Protocol: Protocol, Op: "discover",
		Config: &tailsplit.Config{Tailscale: &tailsplit.TailscaleConfig{}, TUN: tailsplit.TUNConfig{Enabled: true}},
	})
	var event map[string]any
	if err := decoder.Decode(&event); err != nil || event["event"] != "discovered" {
		t.Fatal("device discovery did not finish:", event, err)
	}
	config := <-called
	if config.TUN.Enabled || config.Tailscale.ExitNode != "" {
		t.Fatal("discovery requires an exit or can start TUN")
	}
	_ = connection.Close()
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func session(t *testing.T, path string, request Request) (net.Conn, *json.Decoder) {
	t.Helper()
	connection, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		t.Fatal(err)
	}
	return connection, json.NewDecoder(connection)
}

func TestServiceAuthenticatesPeerAndNeverRunsForAnotherUser(t *testing.T) {
	path, _, cancel, done := testServer(t, os.Getuid()+1, func(context.Context, tailsplit.Config, io.Writer) error {
		t.Error("unauthorized request started the engine")
		return nil
	})
	connection, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_ = connection.SetReadDeadline(time.Now().Add(time.Second))
	var reply [1]byte
	if _, err := connection.Read(reply[:]); err == nil {
		t.Fatal("the service replied to another local user")
	}
	_ = connection.Close()
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestAppDisconnectStopsTheCoreAndWaitsForCleanup(t *testing.T) {
	started := make(chan tailsplit.Config, 1)
	cleaned := make(chan struct{})
	path, server, cancel, done := testServer(t, os.Getuid(), func(ctx context.Context, config tailsplit.Config, output io.Writer) error {
		started <- config
		_ = json.NewEncoder(output).Encode(map[string]string{"event": "ready"})
		<-ctx.Done()
		close(cleaned)
		return nil
	})
	config := &tailsplit.Config{StateDir: "/must-not-be-opened", TUN: tailsplit.TUNConfig{Enabled: true}}
	connection, events := session(t, path, Request{Protocol: Protocol, Op: "start", Config: config,
		Files: Files{TailscaleState: []byte("saved-test-device")}})
	var ready map[string]string
	if err := events.Decode(&ready); err != nil || ready["event"] != "ready" {
		t.Fatalf("core did not start: %v %v", ready, err)
	}
	actual := <-started
	if actual.StateDir != runtimeDirectory(server.Base, os.Getuid()) {
		t.Fatal("the root engine received a client-controlled state path")
	}
	saved, err := os.ReadFile(filepath.Join(actual.StateDir, "tailscale", "tailscaled.state"))
	if err != nil || string(saved) != "saved-test-device" {
		t.Fatal("existing device login was not migrated")
	}
	// A simultaneous app cannot start a competing TUN over the same machine.
	other, reply := session(t, path, Request{Protocol: Protocol, Op: "start", Config: config})
	var busy map[string]string
	if err := reply.Decode(&busy); err != nil || busy["event"] != "error" {
		t.Fatal("a second app was allowed to compete for the core", err)
	}
	_ = other.Close()
	_ = connection.Close()
	select {
	case <-cleaned:
	case <-time.After(time.Second):
		t.Fatal("the engine survived the app's channel disappearing")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestStopInTheSameWriteAsStartIsNotLost(t *testing.T) {
	cleaned := make(chan struct{})
	path, _, cancel, done := testServer(t, os.Getuid(), func(ctx context.Context, _ tailsplit.Config, _ io.Writer) error {
		<-ctx.Done()
		close(cleaned)
		return nil
	})
	connection, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	data, _ := json.Marshal(Request{Protocol: Protocol, Op: "start", Config: &tailsplit.Config{}})
	_, err = connection.Write(append(data, []byte("\nstop\n")...))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-cleaned:
	case <-time.After(time.Second):
		t.Fatal("buffered stop was swallowed by the JSON request parser")
	}
	_, _ = io.Copy(io.Discard, connection)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestServiceExitCancelsAnActiveCore(t *testing.T) {
	started := make(chan struct{})
	cleaned := make(chan struct{})
	path, _, cancel, done := testServer(t, os.Getuid(), func(ctx context.Context, _ tailsplit.Config, _ io.Writer) error {
		close(started)
		<-ctx.Done()
		close(cleaned)
		return nil
	})
	connection, _ := session(t, path, Request{Protocol: Protocol, Op: "start", Config: &tailsplit.Config{}})
	defer connection.Close()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("test core did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("service shutdown did not wait for engine cleanup")
	}
	select {
	case <-cleaned:
	default:
		t.Fatal("service exited while its engine was still active")
	}
}

func TestHelloReportsMatchingServiceWithoutStartingAnEngine(t *testing.T) {
	path, server, cancel, done := testServer(t, os.Getuid(), func(context.Context, tailsplit.Config, io.Writer) error {
		t.Error("status probe started an engine")
		return nil
	})
	info, err := probe(context.Background(), path, strconv.Itoa(os.Getuid()))
	if err != nil || info.BuildID != server.BuildID || info.Initialized {
		t.Fatalf("unexpected installed service: %+v %v", info, err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestLoginUsesOnlyAuthenticationAndDoesNotStartTheNetwork(t *testing.T) {
	path, server, cancel, done := testServer(t, os.Getuid(), func(context.Context, tailsplit.Config, io.Writer) error {
		t.Error("Login started the network runner")
		return nil
	})
	server.Login = func(_ context.Context, config tailsplit.Config, output io.Writer) error {
		if config.TUN.Enabled {
			t.Error("Login retained TUN privileges from the stored config")
		}
		return json.NewEncoder(output).Encode(map[string]string{"event": "authenticated"})
	}
	connection, _ := session(t, path, Request{Protocol: Protocol, Op: "login",
		Config: &tailsplit.Config{TUN: tailsplit.TUNConfig{Enabled: true}}})
	var output bytes.Buffer
	if err := Forward(context.Background(), connection, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"authenticated"`) {
		t.Fatal("login completion was lost")
	}
	_ = connection.Close()
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestServicePolicyCannotReadUserPathsOrBindReservedPorts(t *testing.T) {
	for name, config := range map[string]tailsplit.Config{
		"local rules": {Rules: tailsplit.RulesConfig{GFWList: "/etc/master.passwd"}},
		"environment key": {Tailscale: &tailsplit.TailscaleConfig{
			ExitNode: "100.64.0.1", AuthKeyEnv: "ARBITRARY_ENV"}},
		"privileged proxy port": {Listen: "127.0.0.1:22"},
		"chosen TUN name":       {TUN: tailsplit.TUNConfig{Enabled: true, Name: "../../tmp/arbitrary"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := serviceConfig(config, t.TempDir()); err == nil {
				t.Fatal("unsafe service policy was accepted")
			}
			if name != "local rules" && ValidateAppConfig(config) == nil {
				t.Fatal("app validator accepted a policy the service would reject")
			}
		})
	}
	local := tailsplit.Config{
		StateDir: t.TempDir(), Tailscale: &tailsplit.TailscaleConfig{ExitNode: "100.64.0.1"},
		Rules: tailsplit.RulesConfig{GFWList: "/not-opened/by-validation"},
	}
	if err := ValidateAppConfig(local); err != nil || local.Rules.GFWList != "/not-opened/by-validation" {
		t.Fatal("app validation rejected or rewrote rules transferred as contents:", err)
	}
	config, err := serviceConfig(tailsplit.Config{StateDir: "/etc"}, t.TempDir())
	if err != nil || config.StateDir == "/etc" {
		t.Fatal("state_dir escaped the service-owned directory")
	}
}

func TestServiceRulesUseOnlyTransferredCache(t *testing.T) {
	for _, source := range []string{"builtin", "https://raw.githubusercontent.com/gfwlist/gfwlist/master/gfwlist.txt", "https://rules.example/subscription"} {
		t.Run(source, func(t *testing.T) {
			directory := privateTestDirectory(t)
			config, err := serviceConfig(tailsplit.Config{
				Tailscale: &tailsplit.TailscaleConfig{ExitNode: "100.64.0.10"},
				Rules:     tailsplit.RulesConfig{GFWList: source},
			}, directory)
			if err != nil {
				t.Fatal(err)
			}
			cache := filepath.Join(directory, "gfwlist.txt")
			if config.Rules.GFWList != cache {
				t.Fatal("the service retained authority to download remote rules:", config.Rules.GFWList)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := tailsplit.CompileContext(ctx, config); !os.IsNotExist(err) {
				t.Fatal("missing service cache must fail locally without a download:", err)
			}
			if err := os.WriteFile(cache, []byte("[AutoProxy 0.2.9]\n||fixture.example\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := tailsplit.CompileContext(ctx, config); err != nil {
				t.Fatal("transferred rules could not compile without network access:", err)
			}
		})
	}
}

func TestSavedLoginIsImportedOnceAndCannotReappearAfterLogout(t *testing.T) {
	base := privateTestDirectory(t)
	uid := os.Getuid()
	directory, err := prepareRuntime(base, uid, Files{TailscaleState: []byte("old"), Cache: []byte("fakeip")})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "tailscale", "tailscaled.state")
	if err := os.WriteFile(path, []byte("logged-out"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareRuntime(base, uid, Files{TailscaleState: []byte("old-copy")}); err != nil {
		t.Fatal(err)
	}
	saved, _ := os.ReadFile(path)
	if string(saved) != "logged-out" {
		t.Fatal("a stale user-state copy silently restored a logged-out account")
	}
	info, _ := os.Stat(directory)
	if info.Mode().Perm() != 0o700 {
		t.Fatal("service login state is accessible to other users")
	}
}

func TestChangingRuleSourceDoesNotReuseAnOldServiceCache(t *testing.T) {
	base := privateTestDirectory(t)
	directory, err := prepareRuntime(base, os.Getuid(), Files{Rules: []byte("||custom.example")})
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(directory, "gfwlist.txt")
	if data, err := os.ReadFile(cache); err != nil || string(data) != "||custom.example" {
		t.Fatal("local rule contents did not reach the fixed service cache")
	}
	if _, err := prepareRuntime(base, os.Getuid(), Files{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Fatal("a stale service cache would override the selected builtin policy")
	}
}

func TestUserFilesSelectsTheConfiguredSubscriptionCache(t *testing.T) {
	config := tailsplit.Config{StateDir: t.TempDir(), Rules: tailsplit.RulesConfig{GFWList: "https://rules.example/a"}}
	cacheA := tailsplit.RulesCachePath(config)
	config.Rules.GFWList = "https://rules.example/b"
	cacheB := tailsplit.RulesCachePath(config)
	if cacheA == cacheB {
		t.Fatal("different subscriptions share one cache pathname")
	}
	for path, data := range map[string]string{
		cacheA: "[AutoProxy 0.2.9]\n||a.fixture.example\n",
		cacheB: "[AutoProxy 0.2.9]\n||b.fixture.example\n",
		filepath.Join(config.StateDir, "gfwlist.txt"): "unrelated legacy cache",
	} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	files, err := userFiles(&config, ImportPlan{})
	if err != nil || string(files.Rules) != "[AutoProxy 0.2.9]\n||b.fixture.example\n" {
		t.Fatal("desktop transfer selected a different subscription's cache:", err)
	}
}

func TestMissingLocalRulesDoNotSilentlySelectBuiltin(t *testing.T) {
	directory := t.TempDir()
	config := tailsplit.Config{StateDir: directory, Rules: tailsplit.RulesConfig{GFWList: filepath.Join(directory, "missing")}}
	if _, err := userFiles(&config, ImportPlan{}); err == nil {
		t.Fatal("a missing custom policy silently selected builtin routes")
	}
	config.Rules.GFWList = "builtin"
	if _, err := userFiles(&config, ImportPlan{}); err != nil {
		t.Fatal("device login and discovery must allow a missing user rules cache")
	}
}

func TestGlobalServiceRequestDoesNotOpenIgnoredSplitRules(t *testing.T) {
	config := tailsplit.Config{
		StateDir: t.TempDir(),
		Rules:    tailsplit.RulesConfig{Mode: tailsplit.RoutingGlobal, GFWList: "/missing-split-rules"},
	}
	files, err := userFiles(&config, ImportPlan{})
	if err != nil || len(files.Rules) != 0 || config.Rules.GFWList != "" {
		t.Fatal("global mode still requires or transfers a split rule source:", err)
	}
}

func TestServiceRejectsSymlinkStorageAndReplacesFilesWithoutFollowingLinks(t *testing.T) {
	base := privateTestDirectory(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(base, "users")); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareRuntime(base, os.Getuid(), Files{}); err == nil {
		t.Fatal("service followed a symlink into client-controlled storage")
	}
	target := filepath.Join(outside, "keep")
	if err := os.WriteFile(target, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := replacePrivate(link, []byte("new")); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(target)
	if string(data) != "unchanged" {
		t.Fatal("service overwrote a symlink's target")
	}
}

func TestServiceRequestHasStrictFieldsAndBoundedInput(t *testing.T) {
	for _, value := range []string{
		`{"protocol":1,"op":"start","command":"sh"}` + "\n",
		`{"protocol":1,"op":"start","config":{"command":"sh"}}` + "\n",
		`{"protocol":1,"op":"start","config":{"DNS":{"Listen":"127.0.0.1:15353"}}}` + "\n",
		`{"protocol":1,"op":"start","config":{"DNS":{"Listen":""}}}` + "\n",
		`{"protocol":1,"op":"hello"} {"second":true}` + "\n",
		`{"protocol":2,"op":"hello"}` + "\n",
		strings.Repeat("x", maxRequest+1),
	} {
		if _, err := readRequest(bufio.NewReader(strings.NewReader(value))); err == nil {
			t.Fatal("invalid or oversized service request was accepted")
		}
	}
	data, _ := json.Marshal(Request{Protocol: Protocol, Op: "start", Files: Files{TailscaleState: bytes.Repeat([]byte("x"), maxState+1)}})
	if _, err := readRequest(bufio.NewReader(bytes.NewReader(append(data, '\n')))); err == nil {
		t.Fatal("oversized authentication state was accepted")
	}
}
