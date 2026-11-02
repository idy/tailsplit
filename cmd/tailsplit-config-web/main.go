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
		snapshot := tray.Snapshot{State: "stopped"}
		startup := false
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
				snapshot = tray.Snapshot{State: "running", Mode: "proxy", Active: true}
			case "disconnect", "logout":
				snapshot = tray.Snapshot{State: "stopped"}
			case "login":
				snapshot = tray.Snapshot{State: "authenticated"}
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
