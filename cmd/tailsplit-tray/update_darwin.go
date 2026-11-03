//go:build darwin

package main

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/idy/tailsplit/internal/appupdate"
)

//go:embed update-macos.sh
var darwinUpdateScript string

func installedDarwinApp(executable string) (string, error) {
	if filepath.Base(executable) != "tailsplit-tray" || filepath.Base(filepath.Dir(executable)) != "MacOS" || filepath.Base(filepath.Dir(filepath.Dir(executable))) != "Contents" {
		return "", errors.New("请从已安装的 TailSplit.app 升级")
	}
	app := filepath.Dir(filepath.Dir(filepath.Dir(executable)))
	if filepath.Ext(app) != ".app" || strings.Contains(app, "/AppTranslocation/") || strings.HasPrefix(app, "/Volumes/TailSplit") {
		return "", errors.New("请先将 TailSplit.app 移到 Applications 再升级")
	}
	info, err := os.Lstat(app)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("App 更新目标不能是符号链接")
	}
	return app, nil
}

func plistValue(ctx context.Context, app, key string) (string, error) {
	value, err := exec.CommandContext(ctx, "/usr/bin/plutil", "-extract", key, "raw", "-o", "-", filepath.Join(app, "Contents", "Info.plist")).Output()
	return strings.TrimSpace(string(value)), err
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

func installDarwinUpdate(ctx context.Context, update appupdate.Package, executable, config, log string, shutdown func(func() error) error) error {
	target, err := installedDarwinApp(executable)
	if err != nil {
		return err
	}
	config, err = filepath.Abs(config)
	if err != nil {
		return err
	}
	log, err = filepath.Abs(log)
	if err != nil {
		return err
	}
	if err := validateDarwinUpdateArchive(update.Path); err != nil {
		return err
	}
	prepareCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	extracted := filepath.Join(update.Directory, "extracted")
	if err := exec.CommandContext(prepareCtx, "/usr/bin/ditto", "-x", "-k", update.Path, extracted).Run(); err != nil {
		return fmt.Errorf("无法解压更新: %w", err)
	}
	app := filepath.Join(extracted, "TailSplit.app")
	identity, err := plistValue(prepareCtx, app, "CFBundleIdentifier")
	if err != nil || identity != "im.lbv.tailsplit" {
		return errors.New("更新包的 App 标识不正确")
	}
	version, err := plistValue(prepareCtx, app, "CFBundleShortVersionString")
	if err != nil || version != update.Version {
		return errors.New("更新包内版本与 Release 不一致")
	}
	// A signed installation accepts only the same Developer ID team. Local
	// ad-hoc builds still require a trusted, notarized release via Gatekeeper.
	requirement := `anchor apple generic and identifier "im.lbv.tailsplit"`
	details, _ := exec.CommandContext(prepareCtx, "/usr/bin/codesign", "-d", "--verbose=4", target).CombinedOutput()
	for _, line := range strings.Split(string(details), "\n") {
		if strings.HasPrefix(line, "TeamIdentifier=") {
			team := strings.TrimPrefix(line, "TeamIdentifier=")
			if len(team) == 10 && strings.IndexFunc(team, func(r rune) bool { return (r < 'A' || r > 'Z') && (r < '0' || r > '9') }) < 0 {
				requirement += ` and certificate leaf[subject.OU] = "` + team + `"`
			}
		}
	}
	if err := exec.CommandContext(prepareCtx, "/usr/bin/codesign", "--verify", "--deep", "--strict", "-R", requirement, app).Run(); err != nil {
		return errors.New("新版 App 的签名校验失败")
	}
	if err := exec.CommandContext(prepareCtx, "/usr/sbin/spctl", "--assess", "--type", "execute", app).Run(); err != nil {
		return errors.New("新版 App 未通过 macOS Gatekeeper 校验")
	}
	args := []string{update.Directory, target, strconv.Itoa(os.Getpid()), strconv.Itoa(os.Getuid()), config, log, requirement, update.Version}
	command := exec.Command("/bin/sh", append([]string{"-c", darwinUpdateScript, "tailsplit-updater"}, args...)...)
	if syscall.Access(filepath.Dir(target), 2) != nil {
		values := []string{"/bin/sh", "-c", darwinUpdateScript, "tailsplit-updater"}
		values = append(values, args...)
		for i := range values {
			values[i] = shellQuote(values[i])
		}
		// AppleScript's default two-minute event timeout could expire while the
		// password prompt is open; authorizationCtx bounds the wait instead.
		command = exec.Command("/usr/bin/osascript", "-e", "on run argv\n with timeout of 3600 seconds\n  do shell script (item 1 of argv) with administrator privileges\n end timeout\nend run", strings.Join(values, " "))
	}
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		return err
	}
	finished := make(chan error, 1)
	go func() { finished <- command.Wait() }()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	authorizationCtx, stop := context.WithTimeout(ctx, 5*time.Minute)
	defer stop()
	for {
		select {
		case <-authorizationCtx.Done():
			_ = command.Process.Kill()
			<-finished
			return authorizationCtx.Err()
		case err := <-finished:
			return fmt.Errorf("无法准备 App 替换（可能取消了管理员授权）: %v %s", err, strings.TrimSpace(output.String()))
		case <-ticker.C:
			if _, err := os.Stat(filepath.Join(update.Directory, "ready")); err == nil {
				// The detached worker owns staging and cleanup after this handoff.
				return shutdown(func() error { return os.WriteFile(filepath.Join(update.Directory, "proceed"), nil, 0o600) })
			}
		}
	}
}
