// Command tailsplit-config-web previews the configuration page against an
// explicitly selected local copy. It never starts or reconnects the proxy.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/appupdate"
	"github.com/idy/tailsplit/internal/configweb"
	"github.com/idy/tailsplit/internal/tray"
)

func main() {
	config := flag.String("config", "", "configuration copy to edit (required)")
	listen := flag.String("listen", "127.0.0.1:19790", "local preview address")
	desktop := flag.Bool("desktop", false, "preview desktop controls without network operations")
	flag.Parse()
	if *config == "" {
		fmt.Fprintln(os.Stderr, "Pass --config with an explicit preview configuration file.")
		os.Exit(2)
	}
	var mutex sync.Mutex
	options := configweb.Options{
		ConfigPath: *config, Listen: *listen, Preview: true,
		Apply: func(ctx context.Context, update func() error) error {
			mutex.Lock()
			defer mutex.Unlock()
			if err := ctx.Err(); err != nil {
				return err
			}
			return update()
		},
	}
	if *desktop {
		snapshot := tray.Snapshot{State: "stopped", BackendState: "NeedsLogin"}
		previewLogin := func(network string) {
			exitIP := "100.64.0.10"
			if network == "preview-work.ts.net" {
				exitIP = "100.64.0.20"
			}
			snapshot = tray.Snapshot{
				State: "authenticated", BackendState: "Running", NetworkName: network,
				ExitNodes: []tailsplit.ExitNode{{ID: exitIP, Name: "preview-exit." + network, Online: true}},
			}
		}
		startup := false
		updates := appupdate.State{CurrentVersion: "0.6.1", Automatic: true}
		options.UpdateState = func() appupdate.State { mutex.Lock(); defer mutex.Unlock(); return updates }
		options.UpdateAction = func(ctx context.Context, action string) error {
			mutex.Lock()
			defer mutex.Unlock()
			if err := ctx.Err(); err != nil {
				return err
			}
			switch action {
			case "check":
				updates.LatestVersion = "0.6.2"
				updates.Available = updates.CurrentVersion != updates.LatestVersion
				updates.LastChecked = time.Date(2026, time.November, 3, 12, 0, 0, 0, time.UTC)
				updates.ReleaseURL = "https://github.com/idy/tailsplit/releases/tag/v0.6.2"
				updates.ReleaseNotes = "TailSplit 0.6.2\n\n预览版本说明：改进应用更新体验。\n安装操作仅模拟版本切换，不会下载或运行安装程序。"
			case "install":
				if !updates.Available {
					return fmt.Errorf("请先检查可用更新。")
				}
				updates.CurrentVersion = updates.LatestVersion
				updates.Available = false
			case "automatic-on":
				updates.Automatic = true
			case "automatic-off":
				updates.Automatic = false
			}
			return nil
		}
		options.Snapshot = func() tray.Snapshot { mutex.Lock(); defer mutex.Unlock(); return snapshot }
		options.Startup = func() bool { mutex.Lock(); defer mutex.Unlock(); return startup }
		options.Logs = func() (string, error) {
			return "UI preview only. No proxy, TUN, network service or Tailnet connection is started.\n", nil
		}
		options.Action = func(ctx context.Context, action string) error {
			mutex.Lock()
			defer mutex.Unlock()
			if err := ctx.Err(); err != nil {
				return err
			}
			switch action {
			case "connect":
				snapshot.State, snapshot.Mode, snapshot.Active = "running", "proxy", true
			case "disconnect":
				snapshot.State, snapshot.Mode, snapshot.Active = "stopped", "", false
			case "logout":
				snapshot = tray.Snapshot{State: "stopped", BackendState: "NeedsLogin"}
			case "login":
				previewLogin("preview-personal.ts.net")
			case "switch-network":
				original, revision, err := tray.ReadConfigDocument(*config)
				if err != nil {
					return err
				}
				updated, err := tray.PatchConfigDocument(original,
					tray.ConfigChange{Section: "tailscale", Key: "tailnet", Value: ""},
					tray.ConfigChange{Section: "tailscale", Key: "exit_node", Value: ""},
					tray.ConfigChange{Section: "tailscale", Key: "auth_key_env", Value: ""},
				)
				if err != nil {
					return err
				}
				if err := tray.SaveConfigDocument(*config, revision, updated); err != nil {
					return err
				}
				network := "preview-work.ts.net"
				if snapshot.NetworkName == network {
					network = "preview-personal.ts.net"
				}
				previewLogin(network)
			case "startup-on":
				startup = true
			case "startup-off":
				startup = false
			}
			return nil
		}
	}
	server, err := configweb.Start(options)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer server.Close()
	fmt.Println("Local configuration preview:", server.URL())
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	<-ctx.Done()
}
