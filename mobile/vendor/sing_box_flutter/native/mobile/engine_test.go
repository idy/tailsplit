// SPDX-License-Identifier: GPL-3.0-or-later
package libbox

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/certificate"
	"github.com/sagernet/sing-box/adapter/endpoint"
	"github.com/sagernet/sing-box/adapter/inbound"
	"github.com/sagernet/sing-box/adapter/outbound"
	boxservice "github.com/sagernet/sing-box/adapter/service"
	"github.com/sagernet/sing-box/dns"
	"github.com/sagernet/sing-box/dns/transport"
	"github.com/sagernet/sing-box/experimental/cachefile"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/protocol/direct"
	"github.com/sagernet/sing-box/protocol/group"
	"github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/common/json/badoption"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/filemanager"
	"github.com/sagernet/sing/service/pause"
)

func cacheTestContext(dir string) context.Context {
	outbounds := outbound.NewRegistry()
	direct.RegisterOutbound(outbounds)
	group.RegisterSelector(outbounds)
	dnsRegistry := dns.NewTransportRegistry()
	transport.RegisterUDP(dnsRegistry)
	ctx := service.ContextWithDefaultRegistry(context.Background())
	ctx = filemanager.WithDefault(ctx, dir, dir, os.Getuid(), os.Getgid())
	return box.Context(ctx, inbound.NewRegistry(), outbounds, endpoint.NewRegistry(), dnsRegistry, boxservice.NewRegistry(), certificate.NewRegistry())
}

func startCachedSelector(t *testing.T, dir string, cacheOptions *option.CacheFileOptions) (*group.Selector, adapter.CacheFile, func()) {
	t.Helper()
	ctx := cacheTestContext(dir)
	options, err := json.UnmarshalExtendedContext[option.Options](ctx, []byte(`{"outbounds":[{"type":"selector","tag":"choose","outbounds":["defaultA","proxyB"],"default":"defaultA"},{"type":"direct","tag":"defaultA"},{"type":"direct","tag":"proxyB"}],"route":{"final":"choose"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if cacheOptions != nil {
		options.Experimental = &option.ExperimentalOptions{CacheFile: cacheOptions}
	}
	var original option.CacheFileOptions
	if cacheOptions != nil {
		original = *cacheOptions
	}
	mobile, err := newBoxEngine(ctx, &monitorTestPlatform{}, options)
	if err != nil {
		t.Fatal(err)
	}
	engine := mobile.(*boxEngine)
	t.Cleanup(func() { _ = engine.Close() })
	if cacheOptions != nil && !reflect.DeepEqual(*cacheOptions, original) {
		t.Fatal("mobile defaults mutated the caller's cache options")
	}
	cache := service.FromContext[adapter.CacheFile](engine.ctx)
	if cache == nil {
		t.Fatal("mobile engine did not register its default cache")
	}
	// Start only the real cache and selector stages needed for persistence. No
	// TUN, monitor, API listener or network endpoint is started by this test.
	scope := adapter.NewScope(engine.ctx, log.NewNOPFactory().Logger())
	t.Cleanup(func() { _ = scope.Close() })
	if err := scope.Start("cache", cache, adapter.StartStateInitialize); err != nil {
		t.Fatal(err)
	}
	selected, loaded := engine.box.Outbound().Outbound("choose")
	if !loaded {
		t.Fatal("selector missing")
	}
	selector := selected.(*group.Selector)
	if err := scope.Start("selector", selector, adapter.StartStateStart); err != nil {
		t.Fatal(err)
	}
	var closeOnce sync.Once
	close := func() {
		closeOnce.Do(func() {
			if err := scope.Close(); err != nil {
				t.Error(err)
			}
			if err := engine.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	return selector, cache, close
}

func TestMobileCachePersistsSelectorForAllEnableSettings(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options *option.CacheFileOptions
	}{
		{"absent", nil},
		{"explicit-false", &option.CacheFileOptions{Enabled: false}},
		{"explicit-true", &option.CacheFileOptions{Enabled: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			first, _, closeFirst := startCachedSelector(t, dir, tc.options)
			defer closeFirst()
			if first.Selected(N.NetworkTCP).Tag() != "defaultA" || !first.SelectOutbound("proxyB") {
				t.Fatal("could not change the selector")
			}
			closeFirst()
			second, _, closeSecond := startCachedSelector(t, dir, tc.options)
			defer closeSecond()
			if second.Selected(N.NetworkTCP).Tag() != "proxyB" {
				t.Fatal("mobile selector choice was not restored")
			}
		})
	}
}

func TestMobileCachePreservesPathIDAndFlags(t *testing.T) {
	dir := t.TempDir()
	options := &option.CacheFileOptions{
		Enabled: false, Path: "custom.db", CacheID: "profile-A",
		StoreFakeIP: true, StoreRDRC: true, StoreDNS: true,
		RDRCTimeout: badoption.Duration(time.Hour),
	}
	first, cache, closeFirst := startCachedSelector(t, dir, options)
	defer closeFirst()
	if cache.CacheID() != options.CacheID || !cache.StoreFakeIP() || !cache.StoreRDRC() || !cache.StoreDNS() {
		t.Fatal("custom cache options were lost")
	}
	if _, err := os.Stat(filepath.Join(dir, options.Path)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "cache.db")); !os.IsNotExist(err) {
		t.Fatal("custom cache path was replaced with the default")
	}
	if !first.SelectOutbound("proxyB") {
		t.Fatal("selector change failed")
	}
	closeFirst()
	otherOptions := *options
	otherOptions.CacheID = "profile-B"
	other, _, closeOther := startCachedSelector(t, dir, &otherOptions)
	if other.Selected(N.NetworkTCP).Tag() != "defaultA" {
		t.Fatal("cache IDs no longer isolate selector state")
	}
	closeOther()
	restored, _, closeRestored := startCachedSelector(t, dir, options)
	defer closeRestored()
	if restored.Selected(N.NetworkTCP).Tag() != "proxyB" {
		t.Fatal("custom cache ID did not restore its state")
	}
}

func TestMobileCacheRestoresExistingPinnedCache(t *testing.T) {
	dir := t.TempDir()
	ctx := cacheTestContext(dir)
	logger := log.NewNOPFactory().Logger()
	scope := adapter.NewScope(ctx, logger)
	defer scope.Close()
	// The pinned constructor enabled this service even with Enabled=false.
	cache := cachefile.New(ctx, logger, option.CacheFileOptions{Enabled: false})
	if err := scope.Start("legacy cache", cache, adapter.StartStateInitialize); err != nil {
		t.Fatal(err)
	}
	if err := cache.StoreSelected("choose", "proxyB"); err != nil {
		t.Fatal(err)
	}
	if err := scope.Close(); err != nil {
		t.Fatal(err)
	}
	selector, _, close := startCachedSelector(t, dir, nil)
	defer close()
	if selector.Selected(N.NetworkTCP).Tag() != "proxyB" {
		t.Fatal("existing mobile cache was ignored")
	}
}

func TestPrivateLogPathsStayWithinWorkingStorage(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	ctx := cacheTestContext(root)
	for _, output := range []string{filepath.Join(outside, "activity.log"), "../activity.log", root, "stdout", "stderr"} {
		if _, err := privateLogPath(ctx, &option.LogOptions{Output: output}); err == nil {
			t.Errorf("accepted unsafe log output %q", output)
		}
	}
	for _, output := range []string{"", "logs/activity.log", filepath.Join(root, "absolute.log")} {
		path, err := privateLogPath(ctx, &option.LogOptions{Output: output})
		if err != nil {
			t.Fatal(err)
		}
		file, err := openPrivateLog(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = file.WriteString("started\n"); err != nil {
			t.Fatal(err)
		}
		info, err := file.Stat()
		_ = file.Close()
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("private log permissions: %v %v", info, err)
		}
	}
}

func TestPrivateLogRejectsSymlinkParentsAndTargetsWithoutTouchingVictim(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	ctx := cacheTestContext(root)
	victim := filepath.Join(outside, "private-data")
	if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, target, output string }{
		{"parent", outside, "parent/private-data"},
		{"target", victim, "target"},
		{"internal", filepath.Join(root, "real"), "internal/activity.log"},
	} {
		if err := os.MkdirAll(filepath.Join(root, "real"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(tc.target, filepath.Join(root, tc.name)); err != nil {
			t.Fatal(err)
		}
		if _, err := privateLogPath(ctx, &option.LogOptions{Output: tc.output}); err == nil {
			t.Fatalf("accepted symlink %s", tc.name)
		}
		if file, err := openPrivateLog(ctx, filepath.Join(root, tc.output)); err == nil {
			file.Close()
			t.Fatalf("opened symlink %s", tc.name)
		}
	}
	// Simulate a symlink substituted after validation. Opening must check again.
	path, err := privateLogPath(ctx, &option.LogOptions{Output: "swapped/private-data"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "swapped")); err != nil {
		t.Fatal(err)
	}
	if file, err := openPrivateLog(ctx, path); err == nil {
		file.Close()
		t.Fatal("followed a parent swapped after validation")
	}
	if err := os.Link(victim, filepath.Join(root, "hardlink")); err != nil {
		t.Fatal(err)
	}
	if file, err := openPrivateLog(ctx, filepath.Join(root, "hardlink")); err == nil {
		file.Close()
		t.Fatal("opened a multiply linked file")
	}
	content, err := os.ReadFile(victim)
	if err != nil || string(content) != "keep" {
		t.Fatalf("victim changed: %q %v", content, err)
	}
	info, err := os.Stat(victim)
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatal("victim permissions changed")
	}
}

func TestMobileWakeEndsDevicePauseOnEveryPlatform(t *testing.T) {
	ctx := pause.WithDefaultManager(context.Background())
	manager := service.FromContext[pause.Manager](ctx)
	logFile, err := os.CreateTemp(t.TempDir(), "mobile-*.log")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logFile.Close() })
	engine := &boxEngine{ctx: ctx, pause: manager, log: logFile}
	var events []int
	manager.RegisterCallback(func(event int) { events = append(events, event) })
	// Waking an active core must not emit a spurious wake transition.
	engine.Wake()
	// Pause ends with this same device pause; no host reports screen state.
	manager.DevicePause()
	if !manager.IsPaused() {
		t.Fatal("device pause did not pause the core")
	}
	engine.Wake()
	if manager.IsDevicePaused() || manager.IsPaused() {
		t.Fatal("wake left the core paused")
	}
	if !slices.Equal(events, []int{pause.EventDevicePaused, pause.EventDeviceWake}) {
		t.Fatalf("pause events=%v", events)
	}
	content, err := os.ReadFile(logFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(content), " awake\n") != 2 {
		t.Fatal("wake did not record its lifecycle event")
	}
}
