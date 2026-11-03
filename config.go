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
	DNS       DNSConfig        `yaml:"dns"`
	Rules     RulesConfig      `yaml:"rules"`
	TUN       TUNConfig        `yaml:"tun"`
}

type AppConfig struct {
	ConnectOnStart  bool  `yaml:"connect_on_start"`
	CheckForUpdates *bool `yaml:"check_for_updates"`
}

func (c AppConfig) UpdatesEnabled() bool {
	return c.CheckForUpdates == nil || *c.CheckForUpdates
}

type TailscaleConfig struct {
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
	Mode          string   `yaml:"mode"`
	GFWList       string   `yaml:"gfwlist"`
	ProxyDomains  []string `yaml:"proxy_domains"`
	ProxyIPs      []string `yaml:"proxy_ips"`
	DirectDomains []string `yaml:"direct_domains"`
	DirectIPs     []string `yaml:"direct_ips"`
	Final         string   `yaml:"final"`
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

func Load(path string) (Config, error) {
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
	return config, nil
}

func (c *Config) normalize(base string) error {
	if c.Version == 0 {
		c.Version = 1
	}
	if c.Version != 1 {
		return fmt.Errorf("unsupported config version %d", c.Version)
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
	if c.Rules.Mode == RoutingGlobal && c.Tailscale == nil {
		return errors.New("global routing requires a tailscale endpoint")
	}
	if c.Rules.Final == "" {
		c.Rules.Final = "direct"
	}
	if c.Rules.Final != "direct" && c.Rules.Final != "proxy" {
		return errors.New("rules.final must be direct or proxy")
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
	} else if len(c.Rules.ProxyDomains)+len(c.Rules.ProxyIPs)+len(c.DNS.MagicDomains) > 0 ||
		c.Rules.GFWList != "" || c.Rules.Final == "proxy" {
		return errors.New("proxy rules and MagicDNS require a tailscale endpoint")
	}
	if c.Rules.GFWList != "" && c.Rules.GFWList != "builtin" &&
		!strings.HasPrefix(c.Rules.GFWList, "https://") {
		if strings.Contains(c.Rules.GFWList, "://") {
			return errors.New("remote GFWList must use HTTPS")
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
