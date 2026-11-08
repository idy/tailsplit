package tailsplit

import (
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/net/idna"
	"gopkg.in/yaml.v3"
)

// Config contains the supported user-facing policy, not raw sing-box options.
type Config struct {
	Version   int              `yaml:"version"`
	Listen    string           `yaml:"listen"`
	StateDir  string           `yaml:"state_dir"`
	Interface string           `yaml:"interface"`
	LogLevel  string           `yaml:"log_level"`
	App       AppConfig        `yaml:"app"`
	Tailscale *TailscaleConfig `yaml:"tailscale"`
	// Exits adds embedded devices beside the primary tailscale endpoint. Each
	// one logs in separately and uses its own exit node; rules choose among them.
	Exits []ExitConfig `yaml:"exits,omitempty"`
	DNS   DNSConfig    `yaml:"dns"`
	Rules RulesConfig  `yaml:"rules"`
	TUN   TUNConfig    `yaml:"tun"`
	// Focus scopes login, discovery, logout and ping to one exit ("main" is the
	// primary endpoint). It is never stored in YAML; the service re-derives paths.
	Focus string `yaml:"-"`

	// includePending keeps exits without a selected node in device-only
	// sessions, so they can log in and list nodes before one is chosen.
	includePending bool

	rulesCacheSource   string
	rulesCacheStateDir string
}

type AppConfig struct {
	ConnectOnStart  bool  `yaml:"connect_on_start"`
	CheckForUpdates *bool `yaml:"check_for_updates"`
}

func (c AppConfig) UpdatesEnabled() bool {
	return c.CheckForUpdates == nil || *c.CheckForUpdates
}

type TailscaleConfig struct {
	Name       string   `yaml:"name,omitempty"`
	Hostname   string   `yaml:"hostname"`
	ExitNode   string   `yaml:"exit_node"`
	Tailnet    string   `yaml:"tailnet"`
	AuthKeyEnv string   `yaml:"auth_key_env"`
	Domains    []string `yaml:"domains"`
}

// DNSConfig configures upstream resolution. Client DNS is fixed to the TUN peer.
type DNSConfig struct {
	Direct       string   `yaml:"direct"`
	DirectIPv6   *bool    `yaml:"direct_ipv6"`
	Proxy        string   `yaml:"proxy"`
	FakeIP       *bool    `yaml:"fake_ip"`
	MagicDomains []string `yaml:"magic_domains"`
}

type RulesConfig struct {
	// Source is the preferred YAML field. Normalize resolves it into GFWList
	// to preserve the existing app-service wire format and cache ownership.
	Source        *string  `yaml:"source,omitempty" json:"-"`
	Mode          string   `yaml:"mode"`
	GlobalExit    string   `yaml:"global_exit"`
	DefaultExit   string   `yaml:"default_exit"`
	GFWList       string   `yaml:"gfwlist,omitempty"`
	ProxyDomains  []string `yaml:"proxy_domains"`
	ProxyIPs      []string `yaml:"proxy_ips"`
	DirectDomains []string `yaml:"direct_domains"`
	DirectIPs     []string `yaml:"direct_ips"`
	// Final is direct, proxy (the default exit) or an exit ID.
	Final string `yaml:"final"`
	// Routes send additional domains and CIDRs through a chosen exit. Explicit
	// direct rules still take precedence; these precede the implicit proxy rules.
	Routes []RouteConfig `yaml:"routes,omitempty"`
}

// ExitConfig is one additional embedded device. Its state lives under
// state_dir/exits/<id>, so the ID must stay stable once the device logs in.
type ExitConfig struct {
	ID   string `yaml:"id"`
	Name string `yaml:"name,omitempty"`
	// AutoName lets the desktop update the label when its selected node changes.
	AutoName bool   `yaml:"auto_name,omitempty"`
	Hostname string `yaml:"hostname,omitempty"`
	ExitNode string `yaml:"exit_node"`
	Tailnet  string `yaml:"tailnet,omitempty"`
}

type RouteConfig struct {
	Exit    string   `yaml:"exit"`
	Domains []string `yaml:"domains,omitempty"`
	IPs     []string `yaml:"ips,omitempty"`
}

// MainExit names the primary tailscale endpoint in rules, focus and status.
const MainExit = "main"

// UnsetExit records an explicit lack of a traffic exit without colliding with
// a valid device ID. Omitting the setting still preserves the legacy main default.
const UnsetExit = "-"

var exitID = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,30}[a-z0-9])?$`)

// ExitTag is the sing-box endpoint tag for an exit ID.
func ExitTag(id string) string {
	if id == "" || id == MainExit {
		return "tailnet"
	}
	return "exit-" + id
}

// ExitStateDir keeps the primary device at its original location.
func (c Config) ExitStateDir(id string) string {
	if id == "" || id == MainExit {
		return filepath.Join(c.StateDir, "tailscale")
	}
	return filepath.Join(c.StateDir, "exits", id, "tailscale")
}

func (c Config) hasExit(id string) bool {
	if id == MainExit {
		return c.Tailscale != nil
	}
	for _, exit := range c.Exits {
		if exit.ID == id {
			return true
		}
	}
	return false
}

// ExitIDs lists the primary exit first, then additional exits in file order.
func (c Config) ExitIDs() []string {
	var ids []string
	if c.Tailscale != nil {
		ids = append(ids, MainExit)
	}
	for _, exit := range c.Exits {
		ids = append(ids, exit.ID)
	}
	return ids
}

// SplitPingTarget separates an optional "exit|node" ping target.
func SplitPingTarget(target string) (exit, id string) {
	if exit, id, ok := strings.Cut(target, "|"); ok {
		return exit, id
	}
	return "", target
}

const (
	RoutingSplit  = "split"
	RoutingGlobal = "global"
)

type TUNConfig struct {
	Enabled bool   `yaml:"enabled"`
	Name    string `yaml:"name"`
	MTU     uint32 `yaml:"mtu"`
}

var (
	envName       = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	hostnameLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)
	hostnameNoise = regexp.MustCompile(`[^a-z0-9-]+`)
)

// Normalize validates policy and resolves defaults/paths. Callers crossing a
// privilege boundary must first replace filesystem paths with their own paths.
func Normalize(config Config, base string) (Config, error) {
	err := config.normalize(base)
	return config, err
}

// WithRulesCacheOrigin keeps a draft's legacy-cache ownership tied to the saved
// configuration. A zero origin prevents a draft from claiming a shared cache.
func (c Config) WithRulesCacheOrigin(origin Config) Config {
	c.rulesCacheSource = origin.rulesCacheSource
	c.rulesCacheStateDir = origin.rulesCacheStateDir
	return c
}

func Load(path string) (Config, error) {
	return loadConfig(path, true)
}

// LoadDraft validates a candidate without granting it ownership of a legacy cache.
func LoadDraft(path string) (Config, error) {
	return loadConfig(path, false)
}

func loadConfig(path string, saved bool) (Config, error) {
	stream, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer stream.Close()
	info, err := stream.Stat()
	if err != nil {
		return Config{}, err
	}
	if info.Size() > 1<<20 {
		return Config{}, errors.New("config.yaml exceeds 1 MiB")
	}
	decoder := yaml.NewDecoder(io.LimitReader(stream, (1<<20)+1))
	decoder.KnownFields(true)
	var config Config
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("read config.yaml: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return Config{}, errors.New("config.yaml must contain exactly one YAML document")
	}
	base, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return Config{}, err
	}
	if err := config.normalize(base); err != nil {
		return Config{}, err
	}
	if saved {
		config.rulesCacheSource = rulesSubscriptionSource(config.Rules.GFWList)
		config.rulesCacheStateDir = config.StateDir
		// Loading must not fail login or ignored-rule operations. Compilation
		// retries and reports migration errors when the rules are needed.
		_, _ = migrateLegacyRules(config)
	}
	return config, nil
}

func (c *Config) normalize(base string) error {
	if c.Version == 0 {
		c.Version = 1
	}
	if c.Version != 1 {
		return fmt.Errorf("unsupported config version %d", c.Version)
	}
	if c.Rules.Source != nil {
		if c.Rules.GFWList != "" {
			return errors.New("use rules.source or the legacy rules.gfwlist field, not both")
		}
		c.Rules.GFWList = *c.Rules.Source
		c.Rules.Source = nil
	}
	if c.Listen == "" {
		c.Listen = "127.0.0.1:17890"
	}
	if c.StateDir == "" {
		c.StateDir = "~/.local/share/letsolo/tailsplit"
	}
	var err error
	c.StateDir, err = resolvePath(base, c.StateDir)
	if err != nil {
		return err
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	switch c.LogLevel {
	case "trace", "debug", "info", "warn", "error", "fatal", "panic":
	default:
		return errors.New("log_level must be trace, debug, info, warn, error, fatal or panic")
	}
	value, err := netip.ParseAddrPort(c.Listen)
	if err != nil || !value.Addr().IsLoopback() || value.Port() == 0 {
		return errors.New("listen must be a literal loopback IP and nonzero port")
	}
	if c.DNS.Direct == "" {
		c.DNS.Direct = "system"
	}
	if c.DNS.Proxy == "" {
		c.DNS.Proxy = "8.8.8.8"
	}
	for name, address := range map[string]string{"dns.direct": c.DNS.Direct, "dns.proxy": c.DNS.Proxy} {
		if name == "dns.direct" && address == "system" {
			continue
		}
		resolver, err := parseResolver(address)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if isTUNAddress(resolver.Addr()) {
			return fmt.Errorf("%s points back to TailSplit's TUN network", name)
		}
		if resolver.Addr().IsUnspecified() || resolver.Addr().IsMulticast() ||
			resolver.Addr() == netip.MustParseAddr("100.100.100.100") ||
			netip.MustParsePrefix("198.18.0.0/15").Contains(resolver.Addr()) ||
			netip.MustParsePrefix("2001:2::/48").Contains(resolver.Addr()) {
			return fmt.Errorf("%s must be a real upstream DNS address; use dns.magic_domains for Tailscale DNS", name)
		}
	}
	if c.Rules.Mode == "" {
		c.Rules.Mode = RoutingSplit
	}
	if c.Rules.Mode != RoutingSplit && c.Rules.Mode != RoutingGlobal {
		return errors.New("rules.mode must be split or global")
	}
	if c.Rules.Final == "" {
		c.Rules.Final = "direct"
	}
	for name, domains := range map[string]*[]string{
		"rules.proxy_domains": &c.Rules.ProxyDomains, "rules.direct_domains": &c.Rules.DirectDomains,
		"dns.magic_domains": &c.DNS.MagicDomains,
	} {
		if err := normalizeDomains(name, domains); err != nil {
			return err
		}
	}
	for name, prefixes := range map[string][]string{
		"rules.proxy_ips": c.Rules.ProxyIPs, "rules.direct_ips": c.Rules.DirectIPs,
	} {
		if err := validatePrefixes(name, prefixes); err != nil {
			return err
		}
	}
	if c.Tailscale != nil {
		c.Tailscale.Tailnet = strings.TrimSpace(c.Tailscale.Tailnet)
		if len(c.Tailscale.Tailnet) > 253 || strings.ContainsAny(c.Tailscale.Tailnet, "\r\n\x00") {
			return errors.New("tailscale.tailnet must be a Tailnet name or DNS suffix")
		}
		c.Tailscale.Hostname, err = DeviceHostname(c.Tailscale.Hostname)
		if err != nil {
			return err
		}
		c.Tailscale.ExitNode = strings.TrimSpace(c.Tailscale.ExitNode)
		if strings.ContainsAny(c.Tailscale.ExitNode, "\r\n\x00") {
			return errors.New("tailscale.exit_node must be a device name or IP")
		}
		if c.Tailscale.AuthKeyEnv != "" && !envName.MatchString(c.Tailscale.AuthKeyEnv) {
			return errors.New("tailscale.auth_key_env must name an environment variable")
		}
		if err := normalizeDomains("tailscale.domains", &c.Tailscale.Domains); err != nil {
			return err
		}
		c.Tailscale.Name = strings.TrimSpace(c.Tailscale.Name)
		if len(c.Tailscale.Name) > 512 || strings.ContainsAny(c.Tailscale.Name, "\r\n\x00") {
			return errors.New("tailscale.name must be a short single-line label")
		}
	}
	if err := c.normalizeExits(); err != nil {
		return err
	}
	for name, selection := range map[string]*string{
		"rules.global_exit": &c.Rules.GlobalExit, "rules.default_exit": &c.Rules.DefaultExit,
	} {
		*selection = strings.TrimSpace(*selection)
		if *selection == "" {
			*selection = MainExit
		}
		// main remains a valid draft placeholder after the last device is
		// removed. Connection validation still rejects a missing traffic device.
		if *selection != MainExit && *selection != UnsetExit && !c.hasExit(*selection) {
			return fmt.Errorf("%s must name a configured exit", name)
		}
	}
	if c.Rules.Final != "direct" && c.Rules.Final != "proxy" &&
		(c.Rules.Final == MainExit || !c.hasExit(c.Rules.Final) && (len(c.ExitIDs()) > 0 || !exitID.MatchString(c.Rules.Final))) {
		return errors.New("rules.final must be direct, proxy or a configured exit ID")
	}
	for index := range c.Rules.Routes {
		route := &c.Rules.Routes[index]
		route.Exit = strings.TrimSpace(route.Exit)
		if !c.hasExit(route.Exit) && (len(c.ExitIDs()) > 0 || !exitID.MatchString(route.Exit)) {
			return fmt.Errorf("rules.routes[%d].exit must name a configured exit", index)
		}
		name := fmt.Sprintf("rules.routes[%d]", index)
		if err := normalizeDomains(name+".domains", &route.Domains); err != nil {
			return err
		}
		if err := validatePrefixes(name+".ips", route.IPs); err != nil {
			return err
		}
	}
	if c.Focus != "" {
		if err := c.applyFocus(); err != nil {
			return err
		}
	}
	if c.Rules.GFWList != "" && c.Rules.GFWList != "builtin" &&
		!strings.HasPrefix(c.Rules.GFWList, "https://") {
		if strings.Contains(c.Rules.GFWList, "://") {
			return errors.New("remote rule sources must use HTTPS")
		}
		c.Rules.GFWList, err = resolvePath(base, c.Rules.GFWList)
		if err != nil {
			return err
		}
	}
	if c.TUN.MTU == 0 {
		c.TUN.MTU = 1400
	}
	if c.TUN.MTU < 1280 || c.TUN.MTU > 9000 {
		return errors.New("tun.mtu must be between 1280 and 9000")
	}
	return nil
}

func validatePrefixes(name string, prefixes []string) error {
	for _, value := range prefixes {
		prefix, err := netip.ParsePrefix(value)
		if err != nil || prefix != prefix.Masked() || prefix.Addr().Is4In6() {
			return fmt.Errorf("%s contains an invalid or noncanonical CIDR: %s", name, value)
		}
		if prefix.Bits() == 0 {
			return fmt.Errorf("%s must not contain a default route; use rules.final", name)
		}
		if prefix.Overlaps(netip.MustParsePrefix("198.18.0.0/15")) ||
			prefix.Overlaps(netip.MustParsePrefix("2001:2::/48")) {
			return fmt.Errorf("%s overlaps the fake-IP pool: %s", name, value)
		}
	}
	return nil
}

func (c *Config) normalizeExits() error {
	seen := map[string]bool{MainExit: true}
	hostnames := map[string]bool{}
	primaryHostname, err := DeviceHostname("")
	if err != nil {
		return err
	}
	if c.Tailscale != nil {
		primaryHostname = c.Tailscale.Hostname
		hostnames[primaryHostname] = true
	}
	for index := range c.Exits {
		exit := &c.Exits[index]
		exit.ID = strings.TrimSpace(exit.ID)
		if !exitID.MatchString(exit.ID) {
			return fmt.Errorf("exits[%d].id must be 1–32 lowercase letters, digits or hyphens", index)
		}
		if seen[exit.ID] {
			return fmt.Errorf("exits[%d].id %q is already used", index, exit.ID)
		}
		seen[exit.ID] = true
		exit.Name = strings.TrimSpace(exit.Name)
		if len(exit.Name) > 512 || strings.ContainsAny(exit.Name, "\r\n\x00") {
			return fmt.Errorf("exits[%d].name must be a short single-line label", index)
		}
		if exit.Hostname == "" {
			name := primaryHostname
			name = strings.TrimRight(name[:min(len(name), 62-len(exit.ID))], "-") + "-" + exit.ID
			exit.Hostname = name
		}
		var err error
		if exit.Hostname, err = DeviceHostname(exit.Hostname); err != nil {
			return fmt.Errorf("exits[%d].hostname: %w", index, err)
		}
		if hostnames[exit.Hostname] && c.Focus == "" {
			return fmt.Errorf("exits[%d].hostname %q is already used by another exit", index, exit.Hostname)
		}
		hostnames[exit.Hostname] = true
		exit.ExitNode = strings.TrimSpace(exit.ExitNode)
		if strings.ContainsAny(exit.ExitNode, "\r\n\x00") {
			return fmt.Errorf("exits[%d].exit_node must be a device name or IP", index)
		}
		exit.Tailnet = strings.TrimSpace(exit.Tailnet)
		if len(exit.Tailnet) > 253 || strings.ContainsAny(exit.Tailnet, "\r\n\x00") {
			return fmt.Errorf("exits[%d].tailnet must be a Tailnet name or DNS suffix", index)
		}
	}
	return nil
}

// applyFocus selects one runtime device using the primary endpoint's tag.
// Keep the full exit inventory for first-service state migration; compilation
// uses Focus to omit the other endpoints. The service can normalize it again.
func (c *Config) applyFocus() error {
	if !c.hasExit(c.Focus) {
		return errors.New("the selected exit is not configured")
	}
	if c.Focus != MainExit {
		var exit ExitConfig
		for _, candidate := range c.Exits {
			if candidate.ID == c.Focus {
				exit = candidate
			}
		}
		c.Tailscale = &TailscaleConfig{Name: exit.Name, Hostname: exit.Hostname, ExitNode: exit.ExitNode, Tailnet: exit.Tailnet}
		// MagicDNS and custom Tailnet names belong to the primary device.
		c.DNS.MagicDomains = nil
	}
	c.Rules.Routes = nil
	if c.Rules.Final != "direct" {
		c.Rules.Final = "proxy"
	}
	return nil
}

// FocusExit returns a single-device runtime configuration for exit id while
// retaining all additional exits for saved-state migration.
func (c Config) FocusExit(id string) (Config, error) {
	if id == "" {
		return c, nil
	}
	c.Focus = id
	err := c.applyFocus()
	return c, err
}

// TrafficExitID identifies the saved device used for client traffic. Explicit
// device focus takes precedence over the routing preference.
func (c Config) TrafficExitID() string {
	if c.Focus != "" {
		return c.Focus
	}
	selection := c.Rules.DefaultExit
	if c.Rules.Mode == RoutingGlobal {
		selection = c.Rules.GlobalExit
	}
	if strings.TrimSpace(selection) != "" {
		return strings.TrimSpace(selection)
	}
	return MainExit
}

// trafficConfig scopes Global traffic to one saved device without changing the
// caller's policy or migration inventory. Explicit device operations win over
// routing preferences, so login, discovery and ping retain their own focus.
func (c Config) trafficConfig() (Config, error) {
	if c.Rules.Mode != RoutingGlobal || c.Focus != "" {
		return c, nil
	}
	id := c.TrafficExitID()
	focused, err := c.FocusExit(id)
	if err != nil {
		return Config{}, err
	}
	if id != MainExit && strings.HasSuffix(strings.ToLower(focused.Tailscale.Tailnet), ".ts.net") {
		focused.DNS.MagicDomains = []string{strings.ToLower(focused.Tailscale.Tailnet)}
	}
	return focused, nil
}

// DeviceHostname keeps explicitly configured names stable. An empty name follows
// the host, rather than giving every installation the same Tailnet device name.
func DeviceHostname(configured string) (string, error) {
	name := strings.ToLower(strings.TrimSpace(configured))
	if name == "" {
		host, _ := os.Hostname()
		return defaultDeviceHostname(host), nil
	}
	if len(name) > 63 || !hostnameLabel.MatchString(name) {
		return "", errors.New("tailscale.hostname must be a DNS label of 1–63 letters, digits or hyphens")
	}
	return name, nil
}

func defaultDeviceHostname(host string) string {
	host = strings.ToLower(strings.SplitN(strings.TrimSpace(host), ".", 2)[0])
	host = hostnameNoise.ReplaceAllString(host, "-")
	host = strings.Trim(host, "-")
	if host == "" {
		host = "device"
	}
	name := "tailsplit-" + host
	return strings.TrimRight(name[:min(len(name), 63)], "-")
}

func (c Config) fakeIP() bool { return c.DNS.FakeIP == nil || *c.DNS.FakeIP }

func resolvePath(base, path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if path == "~" {
			path = home
		} else {
			path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	return filepath.Clean(path), nil
}

func parseResolver(value string) (netip.AddrPort, error) {
	if address, err := netip.ParseAddr(value); err == nil {
		return netip.AddrPortFrom(address, 53), nil
	}
	address, err := netip.ParseAddrPort(value)
	if err != nil || address.Port() == 0 {
		return netip.AddrPort{}, errors.New("expected an IP address or IP:port")
	}
	return address, nil
}

func normalizeDomains(name string, domains *[]string) error {
	for index, raw := range *domains {
		value := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(raw, "*."), "."), ".")
		value, err := idna.Lookup.ToASCII(value)
		value = strings.ToLower(value)
		if err != nil || !strings.Contains(value, ".") || len(value) > 253 {
			return fmt.Errorf("%s contains an invalid domain suffix", name)
		}
		if _, err := netip.ParseAddr(value); err == nil {
			return fmt.Errorf("%s contains an IP; use the IP ranges field", name)
		}
		for _, label := range strings.Split(value, ".") {
			if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return fmt.Errorf("%s contains an invalid domain suffix", name)
			}
			for _, char := range label {
				if !(char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-') {
					return fmt.Errorf("%s contains an invalid domain suffix", name)
				}
			}
		}
		(*domains)[index] = value
	}
	return nil
}
