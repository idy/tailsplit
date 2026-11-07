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
	server, err := configweb.Start(previewOptions(*config, *listen, *desktop))
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

func previewOptions(configPath, listen string, desktop bool) configweb.Options {
	var mutex sync.Mutex
	snapshot := tray.Snapshot{State: "stopped", BackendState: "NeedsLogin"}
	options := configweb.Options{
		ConfigPath: configPath, Listen: listen, Preview: true,
		Apply: func(ctx context.Context, update func() error) error {
			mutex.Lock()
			defer mutex.Unlock()
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := update(); err != nil {
				return err
			}
			if desktop {
				config, err := tailsplit.Load(configPath)
				if err != nil {
					return err
				}
				// Apply already owns mutex; this only updates the simulated state.
				updatePreviewSnapshot(&snapshot, config)
			}
			return nil
		},
	}
	if desktop {
		// A broken configuration must remain editable in the preview.
		if config, err := tailsplit.Load(configPath); err == nil {
			updatePreviewSnapshot(&snapshot, config)
		}
		previewLogin := func(network string) {
			snapshot.BackendState, snapshot.NetworkName = "Running", network
			if !snapshot.Active {
				snapshot.State = "authenticated"
			}
			snapshot.ExitNodes = []tailsplit.ExitNode{
				{ID: "100.64.0.10", Name: "preview-singapore-exit", Online: true},
				{ID: "100.64.0.20", Name: "preview-tokyo-exit", Online: true},
				{ID: "100.64.0.30", Name: "preview-london-exit-offline", Online: false},
			}
			if network == "preview-work.ts.net" {
				snapshot.ExitNodes = []tailsplit.ExitNode{
					{ID: "100.64.0.20", Name: "preview-work-exit", Online: true},
					{ID: "100.64.0.40", Name: "preview-work-backup", Online: true},
					{ID: "100.64.0.50", Name: "preview-work-offline", Online: false},
				}
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
		options.Snapshot = func() tray.Snapshot {
			mutex.Lock()
			defer mutex.Unlock()
			current := snapshot
			current.ExitNodes = append([]tailsplit.ExitNode(nil), snapshot.ExitNodes...)
			return current
		}
		options.Startup = func() bool { mutex.Lock(); defer mutex.Unlock(); return startup }
		options.Logs = func() (string, error) {
			return "UI preview simulation only. Sign-in, exits and connection state are simulated. No proxy, TUN, network service or Tailnet connection is started.\n\n" +
				"\x1b[37mDEBUG\x1b[0m[0001] config: loaded preview configuration\n" +
				"\x1b[36mINFO\x1b[0m[0001] \x1b[32mTailSplit UI preview is ready\x1b[0m\n" +
				"\x1b[36mINFO\x1b[0m[0002] [\x1b[38;5;168m3717191320\x1b[0m 0ms] inbound/tun[tun-in]: inbound connection from 172.31.255.1:54505\n" +
				"\x1b[36mINFO\x1b[0m[0002] [\x1b[38;5;168m3717191320\x1b[0m 1ms] dns: lookup succeeded for example.com: 192.0.2.10 192.0.2.11\n" +
				"\x1b[36mINFO\x1b[0m[0002] [\x1b[38;5;168m3717191320\x1b[0m 1ms] outbound/direct-ipv4[direct]: outbound connection to example.com:443\n" +
				"\x1b[36mINFO\x1b[0m[0003] [\x1b[38;5;191m24257871\x1b[0m 0ms] inbound/tun[tun-in]: inbound connection to 198.18.0.45:443\n" +
				"\x1b[36mINFO\x1b[0m[0003] [\x1b[38;5;191m24257871\x1b[0m 2ms] endpoint/tailscale[tailnet]: outbound connection to example.org:443\n" +
				"\x1b[36mINFO\x1b[0m[0003] [\x1b[38;5;191m24257871\x1b[0m 259ms] dns: exchanged CNAME example.org. 52 IN CNAME edge.example.org.\n" +
				"\x1b[33mWARN\x1b[0m[0004] preview simulation: selected exit is unavailable\n" +
				"\x1b[31mERROR\x1b[0m[0005] preview simulation: DNS lookup timed out\n", nil
		}
		options.Action = func(ctx context.Context, action string) error {
			mutex.Lock()
			defer mutex.Unlock()
			if err := ctx.Err(); err != nil {
				return err
			}
			switch action {
			case "connect", "login", "refresh-exits":
				config, err := tailsplit.Load(configPath)
				if err != nil {
					return err
				}
				if action != "login" && config.Tailscale != nil && snapshot.BackendState != "Running" {
					return fmt.Errorf("Preview simulation: sign in before connecting or refreshing exits.")
				}
				if action == "connect" {
					snapshot.State, snapshot.Active = "running", true
					if config.Tailscale == nil {
						snapshot.BackendState = "Direct"
					}
				} else {
					network := snapshot.NetworkName
					if network == "" {
						network = "preview-tailnet.example"
					}
					previewLogin(network)
				}
				updatePreviewSnapshot(&snapshot, config)
			case "disconnect":
				snapshot.State, snapshot.Mode, snapshot.Active = "stopped", "", false
			case "logout":
				snapshot = tray.Snapshot{State: "stopped", BackendState: "NeedsLogin", RoutingMode: snapshot.RoutingMode}
			case "switch-network":
				original, revision, err := tray.ReadConfigDocument(configPath)
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
				if err := tray.SaveConfigDocument(configPath, revision, updated); err != nil {
					return err
				}
				updatedConfig, err := tailsplit.Load(configPath)
				if err != nil {
					return err
				}
				network := "preview-work.ts.net"
				if snapshot.NetworkName == network {
					network = "preview-personal.ts.net"
				}
				snapshot = tray.Snapshot{State: "stopped", BackendState: "NeedsLogin"}
				previewLogin(network)
				updatePreviewSnapshot(&snapshot, updatedConfig)
			case "startup-on":
				startup = true
			case "startup-off":
				startup = false
			}
			return nil
		}
	}
	return options
}

// The caller owns the preview mutex. Reading configuration never starts an
// engine, resolves a network address, or opens a real device identity.
func updatePreviewSnapshot(snapshot *tray.Snapshot, config tailsplit.Config) {
	snapshot.RoutingMode = config.Rules.Mode
	snapshot.ExitNode = ""
	if config.Tailscale != nil {
		snapshot.ExitNode = config.Tailscale.ExitNode
	}
	if snapshot.BackendState == "Running" {
		snapshot.Hostname = "preview-device"
		if config.Tailscale != nil {
			snapshot.Hostname = config.Tailscale.Hostname
		}
	}
	for i := range snapshot.ExitNodes {
		exit := &snapshot.ExitNodes[i]
		exit.Selected = snapshot.ExitNode == exit.ID || snapshot.ExitNode == exit.Name
		exit.SelectedFor = ""
		if exit.Selected {
			exit.SelectedFor = snapshot.ExitNode
		}
	}
	if snapshot.Active {
		snapshot.Mode = "proxy"
		if config.TUN.Enabled {
			snapshot.Mode = "tun"
		}
	}
}
