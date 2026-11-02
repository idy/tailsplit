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
)

func main() {
	config := flag.String("config", "", "configuration copy to edit (required)")
	listen := flag.String("listen", "127.0.0.1:19790", "local preview address")
	flag.Parse()
	if *config == "" {
		fmt.Fprintln(os.Stderr, "Pass --config with an explicit preview configuration file.")
		os.Exit(2)
	}
	var mutex sync.Mutex
	server, err := configweb.Start(configweb.Options{
		ConfigPath: *config, Listen: *listen, Preview: true,
		Apply: func(ctx context.Context, update func() error) error {
			mutex.Lock()
			defer mutex.Unlock()
			if err := ctx.Err(); err != nil {
				return err
			}
			return update()
		},
	})
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
