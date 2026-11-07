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
		if !errors.Is(err, ErrNotInstalled) && ctx.Err() == nil {
			_ = json.NewEncoder(output).Encode(map[string]string{"event": "error", "message": err.Error()})
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
	if err == nil && info.BuildID == expected {
		return info, nil
	}
	path := packagePath(executable, InstallPackage)
	if _, err := os.Stat(path); err != nil {
		return Info{}, errors.New("open the packaged TailSplit.app to install its network service")
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
			return Info{}, errors.New("network service installation did not finish; click Start after completing Installer")
		case <-ticker.C:
			info, err := probe(installCtx, SocketPath, "0")
			if err == nil && info.BuildID == expected {
				return info, nil
			}
		}
	}
}

// OpenSession transfers policy and, only during the first migration, a bounded
// copy of the user's saved login/FakeIP cache. It never transfers path authority.
func OpenSession(ctx context.Context, executable, operation string, config tailsplit.Config, output io.Writer) (net.Conn, error) {
	if runtime.GOOS == "windows" {
		return openWindowsSession(ctx, executable, operation, config)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := probe(ctx, SocketPath, "0")
	if operation == "start" {
		info, err = EnsureInstalled(ctx, executable, output)
		if err != nil {
			return nil, err
		}
	}
	if err != nil {
		if _, installedErr := os.Stat(ExecutablePath); installedErr == nil {
			return nil, errors.New("后台服务没有响应；请从“设置与工具 → 后台网络服务 → 更新或修复…”恢复服务")
		}
		return nil, err
	}
	expected, err := executableID(executable)
	if err != nil {
		return nil, err
	}
	if info.BuildID != expected {
		return nil, errors.New("update the TailSplit network service from this app before continuing")
	}
	if operation == "start" {
		if err := tailsplit.EnsureRules(ctx, config, nil); err != nil {
			return nil, err
		}
	}
	files, err := userFiles(&config, !info.Initialized)
	if err != nil {
		return nil, fmt.Errorf("read saved app state for migration: %w", err)
	}
	// Never ask the service to resolve any of the user's filesystem paths.
	config.StateDir = ""
	connection, err := (&net.Dialer{}).DialContext(ctx, "unix", SocketPath)
	if err != nil {
		return nil, err
	}
	if err := requireUser(connection, "0"); err != nil {
		_ = connection.Close()
		return nil, err
	}
	_ = connection.SetWriteDeadline(time.Now().Add(5 * time.Second))
	err = json.NewEncoder(connection).Encode(Request{Protocol: Protocol, Op: operation, Config: &config, Files: files})
	if err != nil {
		_ = connection.Close()
		return nil, err
	}
	_ = connection.SetWriteDeadline(time.Time{})
	return connection, nil
}

// Forward keeps the app channel alive until privileged cleanup completes.
func Forward(ctx context.Context, connection net.Conn, output io.Writer) error {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = connection.SetWriteDeadline(time.Now().Add(time.Second))
			_, _ = io.WriteString(connection, "stop\n")
		case <-done:
		}
	}()
	defer close(done)
	scanner := bufio.NewScanner(connection)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	stopped := false
	for scanner.Scan() {
		line := scanner.Bytes()
		if _, err := output.Write(append(append([]byte(nil), line...), '\n')); err != nil {
			return err
		}
		var event struct {
			Event string `json:"event"`
		}
		if json.Unmarshal(line, &event) == nil && event.Event == "stopped" {
			stopped = true
		}
	}
	if ctx.Err() != nil {
		return nil
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if stopped {
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
