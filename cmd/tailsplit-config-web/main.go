// Command tailsplit-config-web previews the configuration page against an
// explicitly selected local copy. It never starts or reconnects the proxy.
package main

import (
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
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
	operations := newPreviewOperations()
	options := configweb.Options{
		ConfigPath: configPath, Listen: listen, Preview: true,
		Apply: func(ctx context.Context, check func(tray.Snapshot) error, update func() error) error {
			mutex.Lock()
			defer mutex.Unlock()
			if err := ctx.Err(); err != nil {
				return err
			}
			if id := tray.OperationID(ctx); id != "" {
				if !operations.allowed(id) || snapshot.Active && snapshot.OperationID != id {
					return tray.ErrOperationCanceled
				}
				operations.own(&snapshot, id)
			}
			if check != nil {
				if err := check(snapshot); err != nil {
					return err
				}
			}
			if err := update(); err != nil {
				return err
			}
			if desktop {
				data, revision, err := tray.ReadConfigDocument(configPath)
				if err != nil {
					return err
				}
				config, err := tray.ValidateConfigDocument(configPath, data)
				if err != nil {
					return err
				}
				// Apply already owns mutex; this only updates the simulated state.
				updatePreviewSnapshot(&snapshot, config)
				if snapshot.Active && (snapshot.Mode == "proxy" || snapshot.Mode == "tun") {
					snapshot.ActiveRevision = revision
				}
			}
			return nil
		},
	}
	options.RemoveExit = func(ctx context.Context, id, revision string) error {
		mutex.Lock()
		defer mutex.Unlock()
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := tray.RemoveExitDocument(configPath, id, revision, func(id string) error {
			// Preview logout is simulated; no device identity or network is opened.
			snapshot.State, snapshot.Mode, snapshot.Active = "stopped", "", false
			snapshot.ActiveRevision = ""
			if id == tailsplit.MainExit {
				snapshot.BackendState, snapshot.Hostname, snapshot.NetworkName, snapshot.ExitNode = "", "", "", ""
				snapshot.ExitNodes, snapshot.PingAvailable = nil, false
			} else {
				delete(snapshot.Exits, id)
			}
			return nil
		}); err != nil {
			return err
		}
		config, err := tailsplit.Load(configPath)
		if err == nil {
			updatePreviewSnapshot(&snapshot, config)
		}
		return err
	}
	if desktop {
		options.Cancel = func(_ context.Context, request, operation string) error {
			mutex.Lock()
			defer mutex.Unlock()
			operations.cancel(&snapshot, request, operation)
			return nil
		}
		options.ClaimConnectOperation = func(request, operation string) bool {
			mutex.Lock()
			defer mutex.Unlock()
			return operations.claim(&snapshot, request, operation)
		}
		options.ObserveNativeConnectRequest = func(request string) {
			mutex.Lock()
			defer mutex.Unlock()
			operations.offer(&snapshot, request)
		}
		options.ClaimPanelConnectOperation = func(request, operation string, native bool) (bool, bool) {
			mutex.Lock()
			defer mutex.Unlock()
			return operations.claimPanel(&snapshot, request, operation, native)
		}
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
				{ID: "100.64.0.40", Name: "preview-frankfurt-exit", Online: true},
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
			current.Exits = map[string]tray.ExitSnapshot{}
			for id, exit := range snapshot.Exits {
				exit.ExitNodes = append([]tailsplit.ExitNode(nil), exit.ExitNodes...)
				current.Exits[id] = exit
			}
			return current
		}
		options.PingExit = func(ctx context.Context, id string) (tailsplit.ExitPingResult, error) {
			mutex.Lock()
			defer mutex.Unlock()
			if err := ctx.Err(); err != nil {
				return tailsplit.ExitPingResult{}, err
			}
			exit, id := tailsplit.SplitPingTarget(id)
			available, nodes := snapshot.PingAvailable, snapshot.ExitNodes
			if exit != "" {
				available, nodes = snapshot.Exits[exit].PingAvailable, snapshot.Exits[exit].ExitNodes
			}
			if !available {
				return tailsplit.ExitPingResult{}, tailsplit.ErrPingUnavailable
			}
			if err := tailsplit.ValidateExitPingTarget(nodes, id); err != nil {
				return tailsplit.ExitPingResult{}, err
			}
			// Deliberately deterministic simulated values. No network operation,
			// engine, device state or installed service is used by this preview.
			switch id {
			case "100.64.0.20":
				return tailsplit.ExitPingResult{ID: id, LatencyMS: 740, Relay: "preview-simulated"}, nil
			case "100.64.0.40":
				return tailsplit.ExitPingResult{ID: id, LatencyMS: 1250, Relay: "preview-simulated"}, nil
			case "100.101.0.11":
				return tailsplit.ExitPingResult{ID: id, LatencyMS: 61, Relay: "preview-simulated"}, nil
			}
			return tailsplit.ExitPingResult{ID: id, LatencyMS: 23.4, Direct: true}, nil
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
		actionWithRevision := func(ctx context.Context, action, expectedRevision string) (actionErr error) {
			mutex.Lock()
			defer mutex.Unlock()
			defer func() {
				if !snapshot.Active || (snapshot.Mode != "proxy" && snapshot.Mode != "tun") {
					snapshot.ActiveRevision = ""
				}
			}()
			if err := ctx.Err(); err != nil {
				return err
			}
			name, _, _ := strings.Cut(action, ":")
			switch name {
			case "connect", "login", "login-exit", "add-exit", "switch-network", "refresh-exits":
				id := tray.OperationID(ctx)
				if !operations.allowed(id) {
					return tray.ErrOperationCanceled
				}
				// Failed admission and a reused discovery result do not replace
				// the current owner. Publish ownership with successful admission.
				adopt := name != "refresh-exits" || id != "" && !snapshot.Active
				defer func() {
					if actionErr == nil && adopt {
						operations.own(&snapshot, id)
					}
				}()
			}
			if action == "connect" && expectedRevision != "" {
				if err := tray.CheckConfigRevision(configPath, expectedRevision); err != nil {
					return err
				}
			}
			if name, exit, ok := strings.Cut(action, ":"); ok {
				config, err := tailsplit.Load(configPath)
				if err != nil {
					return err
				}
				adding := name == "add-exit"
				if adding {
					candidate, revision, err := tray.PrepareExitAddition(configPath, exit, expectedRevision)
					if err != nil {
						return err
					}
					// Preview authorization is explicitly simulated and immediate;
					// the production controller commits only after real authentication.
					if err := tray.SaveConfigDocument(configPath, revision, candidate); err != nil {
						return err
					}
					config, err = tailsplit.Load(configPath)
					if err != nil {
						return err
					}
					snapshot.AddedExit = exit
					_, snapshot.AddedRevision, _ = tray.ReadConfigDocument(configPath)
					name = "login-exit"
				}
				if snapshot.Exits == nil {
					snapshot.Exits = map[string]tray.ExitSnapshot{}
				}
				// Simulate a second Tailnet so per-exit pickers show different nodes.
				if name == "login-exit" {
					snapshot.State, snapshot.Mode, snapshot.Active = "stopped", "", false
					snapshot.Exits[exit] = tray.ExitSnapshot{BackendState: "Running", NetworkName: "preview-office.ts.net",
						Hostname: "preview-device-" + exit, PingAvailable: true, ExitNodes: []tailsplit.ExitNode{
							{ID: "100.101.0.11", Name: "office-hk-gateway", Online: true},
							{ID: "100.101.0.12", Name: "office-us-gateway", Online: true},
							{ID: "100.101.0.13", Name: "office-lab-offline", Online: false},
						}}
				} else {
					snapshot.Exits[exit] = tray.ExitSnapshot{BackendState: "NeedsLogin"}
				}
				updatePreviewSnapshot(&snapshot, config)
				if adding {
					snapshot.State, snapshot.Mode = "authenticated", "login"
				}
				return nil
			}
			switch action {
			case "connect", "login", "refresh-exits":
				data, revision, err := tray.ReadConfigDocument(configPath)
				if err != nil {
					return err
				}
				if action == "connect" && expectedRevision != "" && revision != expectedRevision {
					return tray.ErrConfigChanged
				}
				config, err := tray.ValidateConfigDocument(configPath, data)
				if err != nil {
					return err
				}
				if action == "refresh-exits" && config.Tailscale != nil && snapshot.BackendState != "Running" {
					return fmt.Errorf("Preview simulation: sign in before refreshing exits.")
				}
				backend := snapshot.BackendState
				if id := config.TrafficExitID(); id != tailsplit.MainExit {
					backend = snapshot.Exits[id].BackendState
				}
				if action == "connect" {
					if err := tailsplit.ValidateTrafficExit(config); err != nil {
						return err
					}
					if len(config.ExitIDs()) > 0 && backend != "Running" {
						return fmt.Errorf("Preview simulation: sign in before connecting.")
					}
					snapshot.State, snapshot.Active = "running", true
					snapshot.ActiveRevision = revision
					if len(config.ExitIDs()) == 0 {
						snapshot.BackendState = "Direct"
					}
				} else if config.Tailscale != nil {
					network := snapshot.NetworkName
					if network == "" {
						network = "preview-tailnet.example"
					}
					previewLogin(network)
				}
				updatePreviewSnapshot(&snapshot, config)
			case "disconnect":
				operations.canceled[snapshot.OperationID] = snapshot.OperationID != ""
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
		options.Action = func(ctx context.Context, action string) error {
			return actionWithRevision(ctx, action, "")
		}
		options.Connect = func(ctx context.Context, revision string) error {
			return actionWithRevision(ctx, "connect", revision)
		}
		options.AddExit = func(ctx context.Context, id, revision string) error {
			return actionWithRevision(ctx, "add-exit:"+id, revision)
		}
	}
	return options
}

// Like the native controller, the simulator retains native bindings after claim
// and cancellation. Its caller owns the preview mutex for every method.
type previewOperations struct {
	canceled, revokedRequests, revokedOperations map[string]bool
	requests                                     map[string]string
	nativeOffers, interruptedRequests            map[string]bool
}

func newPreviewOperations() *previewOperations {
	return &previewOperations{canceled: make(map[string]bool), revokedRequests: make(map[string]bool), revokedOperations: make(map[string]bool), requests: make(map[string]string), nativeOffers: make(map[string]bool), interruptedRequests: make(map[string]bool)}
}

func (o *previewOperations) allowed(id string) bool {
	return !o.canceled[id] && !o.revokedOperations[id]
}

func (o *previewOperations) own(snapshot *tray.Snapshot, id string) {
	if id == "" {
		id = rand.Text()
	}
	if snapshot.OperationID != "" && snapshot.OperationID != id {
		o.canceled[snapshot.OperationID] = true
	}
	snapshot.OperationID = id
}

func (o *previewOperations) claim(snapshot *tray.Snapshot, request, operation string) bool {
	if operation == "" {
		operation = request
	}
	if request == "" || snapshot.ConnectRequest != request || o.revokedRequests[request] || o.canceled[request] || !o.allowed(operation) {
		return false
	}
	o.requests[request] = operation
	snapshot.ConnectRequest = ""
	return true
}

func (o *previewOperations) offer(snapshot *tray.Snapshot, request string) {
	if request != "" && request == snapshot.ConnectRequest {
		o.nativeOffers[request] = true
	}
}

func (o *previewOperations) claimPanel(snapshot *tray.Snapshot, request, operation string, native bool) (bool, bool) {
	if request != "" && o.interruptedRequests[request] {
		return false, true
	}
	if request == "" || request != snapshot.ConnectRequest {
		return false, false
	}
	if !native && o.nativeOffers[request] {
		o.interruptedRequests[request], o.revokedRequests[request], o.revokedOperations[request] = true, true, true
		snapshot.ConnectRequest = ""
		return false, true
	}
	return o.claim(snapshot, request, operation), false
}

func (o *previewOperations) cancel(snapshot *tray.Snapshot, request, operation string) {
	if request != "" {
		o.revokedRequests[request] = true
		if snapshot.ConnectRequest == request && o.requests[request] == "" {
			o.requests[request] = request
		}
		if snapshot.ConnectRequest == request {
			snapshot.ConnectRequest = ""
		}
	}
	bound := o.requests[request]
	if bound != "" {
		o.revokedOperations[request], o.revokedOperations[bound] = true, true
	}
	if operation != "" {
		o.canceled[operation] = true
	}
	if operation != "" && snapshot.OperationID == operation || bound != "" && snapshot.OperationID == bound {
		snapshot.State, snapshot.Mode, snapshot.Active, snapshot.ActiveRevision = "stopped", "", false, ""
	}
}

// The caller owns the preview mutex. Reading configuration never starts an
// engine, resolves a network address, or opens a real device identity.
func updatePreviewSnapshot(snapshot *tray.Snapshot, config tailsplit.Config) {
	snapshot.RoutingMode = config.Rules.Mode
	snapshot.PingAvailable = config.Tailscale != nil && snapshot.BackendState == "Running"
	if config.Tailscale == nil && len(config.ExitIDs()) > 0 {
		snapshot.BackendState, snapshot.Hostname, snapshot.NetworkName = "", "", ""
		snapshot.ExitNodes = nil
	}
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
	markSelected := func(nodes []tailsplit.ExitNode, selected string) {
		for i := range nodes {
			node := &nodes[i]
			node.Selected = selected != "" && (selected == node.ID || selected == node.Name)
			node.SelectedFor = ""
			if node.Selected {
				node.SelectedFor = selected
			}
		}
	}
	markSelected(snapshot.ExitNodes, snapshot.ExitNode)
	if snapshot.Exits == nil {
		snapshot.Exits = map[string]tray.ExitSnapshot{}
	}
	for _, configured := range config.Exits {
		if _, ok := snapshot.Exits[configured.ID]; !ok {
			snapshot.Exits[configured.ID] = tray.ExitSnapshot{BackendState: "NeedsLogin"}
		}
		if state, ok := snapshot.Exits[configured.ID]; ok {
			state.ExitNode = configured.ExitNode
			state.ExitVisible, state.ExitOnline = false, false
			for _, node := range state.ExitNodes {
				if configured.ExitNode != "" && (node.ID == configured.ExitNode || node.Name == configured.ExitNode) {
					state.ExitVisible, state.ExitOnline = true, node.Online
				}
			}
			markSelected(state.ExitNodes, configured.ExitNode)
			snapshot.Exits[configured.ID] = state
		}
	}
	if snapshot.Active {
		snapshot.TrafficExit = config.TrafficExitID()
		snapshot.Mode = "proxy"
		if config.TUN.Enabled {
			snapshot.Mode = "tun"
		}
	}
}
