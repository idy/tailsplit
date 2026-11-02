package appservice

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
		"privileged DNS port":   {DNS: tailsplit.DNSConfig{Listen: "127.0.0.1:80"}},
		"chosen TUN name":       {TUN: tailsplit.TUNConfig{Enabled: true, Name: "../../tmp/arbitrary"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := serviceConfig(config, t.TempDir()); err == nil {
				t.Fatal("unsafe service policy was accepted")
			}
		})
	}
	config, err := serviceConfig(tailsplit.Config{StateDir: "/etc"}, t.TempDir())
	if err != nil || config.StateDir == "/etc" {
		t.Fatal("state_dir escaped the service-owned directory")
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

func TestMissingLocalRulesDoNotSilentlySelectBuiltin(t *testing.T) {
	directory := t.TempDir()
	config := tailsplit.Config{StateDir: directory, Rules: tailsplit.RulesConfig{GFWList: filepath.Join(directory, "missing")}}
	if _, err := userFiles(&config, false); err == nil {
		t.Fatal("a missing custom policy silently selected builtin routes")
	}
	config.Rules.GFWList = "builtin"
	if _, err := userFiles(&config, false); err != nil {
		t.Fatal("the bundled policy incorrectly required a user cache")
	}
}

func TestGlobalServiceRequestDoesNotOpenIgnoredSplitRules(t *testing.T) {
	config := tailsplit.Config{
		StateDir: t.TempDir(),
		Rules:    tailsplit.RulesConfig{Mode: tailsplit.RoutingGlobal, GFWList: "/missing-split-rules"},
	}
	files, err := userFiles(&config, false)
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
