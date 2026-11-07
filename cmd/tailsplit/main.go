package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/appservice"
	"github.com/idy/tailsplit/internal/privatefs"
)

var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	if err := execute(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "tailsplit:", err)
		os.Exit(1)
	}
}

func execute(args []string, out, diagnostic io.Writer) error {
	if handled, err := platformCommand(args, out, diagnostic); handled {
		return err
	}
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(out, "Usage: tailsplit <init|check|render|run|logout|rules refresh|version> [-c config.yaml]")
		return nil
	}
	command := args[0]
	args = args[1:]
	if command == "service" {
		return installedService(args, diagnostic)
	}
	if command == "version" {
		if len(args) != 0 {
			return errors.New("version does not accept arguments")
		}
		fmt.Fprintf(out, "tailsplit %s (commit %s; sing-box %s; tailscale=%t)\n", version, commit, tailsplit.CoreVersion, tailsplit.TailscaleEnabled)
		return nil
	}
	if command == "rules" {
		if len(args) == 0 || args[0] != "refresh" {
			return errors.New("usage: tailsplit rules refresh [-c config.yaml]")
		}
		command = "refresh"
		args = args[1:]
	}
	switch command {
	case "init", "check", "render", "run", "app-run", "app-login", "discover", "app-discover", "logout", "app-logout", "refresh", "ping", "app-ping":
	default:
		return fmt.Errorf("unknown command %q", command)
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(diagnostic)
	path := flags.String("c", "config.yaml", "YAML configuration path")
	target := flags.String("id", "", "discovered exit node ID to ping (ping only)")
	events := flags.Bool("events", false, "emit private connection events on stdout (run only)")
	parentPID := flags.Int("parent-pid", 0, "stop if the owning app exits (run only)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional argument")
	}
	isRun := command == "run" || command == "app-run" || command == "app-login" ||
		command == "discover" || command == "app-discover"
	if *events && !isRun {
		return errors.New("--events is supported only by run")
	}
	if *parentPID < 0 || *parentPID != 0 && (!(isRun || command == "ping" || command == "app-ping") || os.Getppid() != *parentPID) {
		return errors.New("--parent-pid must identify the current parent of a run command")
	}
	if command == "init" {
		file, err := privatefs.CreateFile(*path)
		if err != nil {
			return fmt.Errorf("create config without overwriting existing files: %w", err)
		}
		_, writeErr := file.Write(tailsplit.ExampleYAML)
		if err := errors.Join(writeErr, file.Close()); err != nil {
			return err
		}
		fmt.Fprintln(out, "Created", *path)
		return nil
	}
	config, err := tailsplit.Load(*path)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *parentPID != 0 {
		go watchAppParent(ctx, *parentPID, stop)
	}
	if command == "ping" || command == "app-ping" {
		return runPingCommand(ctx, command == "app-ping", config, *target, out)
	}
	if *target != "" {
		return errors.New("--id is supported only by ping")
	}
	if command == "discover" {
		return runDiscoveryEvents(ctx, config, &eventStream{encoder: json.NewEncoder(out), captureLogs: *events})
	}
	if command == "app-login" || command == "app-discover" {
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		config.TUN.Enabled = false
		operation := "login"
		if command == "app-discover" {
			operation = "discover"
		}
		connection, err := appservice.OpenSession(ctx, executable, operation, config, out)
		if err == nil {
			defer connection.Close()
			return appservice.Forward(ctx, connection, out)
		}
		if !errors.Is(err, appservice.ErrNotInstalled) || runtime.GOOS == "windows" {
			_ = json.NewEncoder(out).Encode(map[string]string{"event": "error", "message": err.Error()})
			return err
		}
		// Device authentication alone never installs or starts the TUN service.
		if command == "app-discover" {
			return runDiscoveryEvents(ctx, config, &eventStream{encoder: json.NewEncoder(out), captureLogs: true})
		}
		return runLoginEvents(ctx, config, &eventStream{encoder: json.NewEncoder(out), captureLogs: true})
	}
	if command == "app-run" {
		if config.Tailscale != nil && config.Tailscale.ExitNode == "" {
			_ = json.NewEncoder(out).Encode(map[string]string{"event": "error", "message": "请先从“选择出口节点”菜单选择一个出口"})
			return errors.New("select an exit node before starting")
		}
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		// Both proxy-only and traffic-capture sessions create a DNS TUN.
		// Device login/discovery still use the listener-free path above.
		return appservice.Launch(ctx, executable, config, out)
	}
	if command == "logout" || command == "app-logout" {
		config = deviceLogoutConfig(config)
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if command == "app-logout" {
			executable, err := os.Executable()
			if err != nil {
				return err
			}
			err = appservice.Logout(ctx, executable, config)
			if err == nil {
				fmt.Fprintln(out, "Signed out the embedded TailSplit service device.")
				return nil
			}
			if !errors.Is(err, appservice.ErrNotInstalled) || runtime.GOOS == "windows" {
				return err
			}
		}
		if err := tailsplit.LogoutSavedTailnet(ctx, config); err != nil {
			return err
		}
		fmt.Fprintln(out, "Signed out the embedded TailSplit device. No TUN or system Tailscale settings were changed.")
		return nil
	}
	if command == "refresh" {
		info, err := tailsplit.RefreshRules(ctx, config, nil)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(info)
	}
	compiled, err := tailsplit.CompileContext(ctx, config)
	if err != nil {
		return err
	}
	if compiled.GFWList != nil {
		fmt.Fprintf(diagnostic, "GFWList: %d proxy, %d direct, %d skipped URL/regex rules\n",
			compiled.GFWList.Proxy, compiled.GFWList.Direct, compiled.GFWList.Skipped)
	}
	switch command {
	case "check":
		if err := tailsplit.Check(compiled); err != nil {
			return err
		}
		fmt.Fprintln(out, "Configuration valid; no listeners, TUN or Tailnet login started.")
		return nil
	case "render":
		_, err := out.Write(compiled.JSON)
		return err
	default:
		if *events {
			return runEvents(ctx, config, compiled, &eventStream{encoder: json.NewEncoder(out)})
		}
		return tailsplit.Run(ctx, config, compiled)
	}
}

// Logout uses only the saved device identity. Keep the on-disk configuration
// unchanged until the controller's revision-checked network switch is committed.
func deviceLogoutConfig(config tailsplit.Config) tailsplit.Config {
	if config.Tailscale != nil {
		endpoint := *config.Tailscale
		endpoint.AuthKeyEnv = ""
		config.Tailscale = &endpoint
	}
	return config
}

func installedService(args []string, diagnostic io.Writer) error {
	if handled, err := platformInstalledService(args); handled {
		return err
	}
	flags := flag.NewFlagSet("service", flag.ContinueOnError)
	flags.SetOutput(diagnostic)
	uid := flags.Int("uid", 0, "desktop user registered by the installer")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected network service argument")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return appservice.RunInstalled(ctx, *uid, func(ctx context.Context, config tailsplit.Config, output io.Writer) error {
		compiled, err := tailsplit.CompileContext(ctx, config)
		if err != nil {
			return err
		}
		return runEvents(ctx, config, compiled, &eventStream{encoder: json.NewEncoder(output), captureLogs: true})
	}, func(ctx context.Context, config tailsplit.Config, output io.Writer) error {
		return runLoginEvents(ctx, config, &eventStream{encoder: json.NewEncoder(output), captureLogs: true})
	}, func(ctx context.Context, config tailsplit.Config, _ io.Writer) error {
		return tailsplit.LogoutSavedTailnet(ctx, config)
	}, func(ctx context.Context, config tailsplit.Config, output io.Writer) error {
		return runDiscoveryEvents(ctx, config, &eventStream{encoder: json.NewEncoder(output), captureLogs: true})
	})
}

func watchParent(ctx context.Context, expected int, current func() int, cancel func()) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if current() != expected {
				cancel()
				return
			}
		}
	}
}
