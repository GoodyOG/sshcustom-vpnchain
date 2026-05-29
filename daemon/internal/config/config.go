// Package config parses and watches settings.ini (shell key="value" format).
package config

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"sync"
)

// Config holds all settings from settings.ini.
type Config struct {
	mu sync.RWMutex

	// SSH connection
	SSHHost     string
	SSHPort     int
	SSHUser     string
	SSHPassword string
	SSHMode     string // direct | sni | sni_http_proxy
	SSHSNIHost  string

	// HTTP proxy (sni_http_proxy mode)
	HTTPProxyHost string
	HTTPProxyPort int

	// Payload injection
	PayloadEnabled bool
	Payload        string

	// Network
	NetworkMode string // redirect | tproxy | tun | tun_udpgw
	SocksPort   int
	TProxyPort  int
	RedirPort   int
	TunDevice   string
	UDPGWServer string

	// Proxy behaviour
	ProxyTCP bool
	ProxyUDP bool
	QUIC     string // disable | enable

	// DNS
	DNSHijackTCP  bool
	DNSHijackUDP  bool
	DNSHijackMode string // redirect | tproxy | disable

	// Speed boost
	ChannelPool     bool
	ChannelPoolSize int
	BBREnabled      bool
	TCPBufferTuning bool

	// IPv6
	IPv6 bool

	// Paths
	BoxDir string
	BoxRun string
	BoxLog string
	BoxPID string
	BinDir string
}

// DefaultConfig returns safe defaults matching settings.ini template.
func DefaultConfig() *Config {
	return &Config{
		SSHPort:         22,
		SSHMode:         "direct",
		HTTPProxyPort:   3128,
		NetworkMode:     "redirect",
		SocksPort:       1080,
		TProxyPort:      9898,
		RedirPort:       9797,
		TunDevice:       "tun0",
		ProxyTCP:        true,
		ProxyUDP:        false,
		QUIC:            "disable",
		DNSHijackTCP:    true,
		DNSHijackUDP:    false,
		DNSHijackMode:   "redirect",
		ChannelPool:     true,
		ChannelPoolSize: 8,
		BoxDir:          "/data/adb/sshcustom",
		BoxRun:          "/data/adb/sshcustom/run",
		BoxLog:          "/data/adb/sshcustom/run/sshcustom.log",
		BoxPID:          "/data/adb/sshcustom/run/sshcustom.pid",
		BinDir:          "/data/adb/sshcustom/bin",
	}
}

// Load parses a settings.ini file into Config.
// Lines are expected in the form: key="value" or key=value
func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	c := DefaultConfig()
	c.mu.Lock()
	defer c.mu.Unlock()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Skip comments, empty lines, and shell constructs
		if line == "" || strings.HasPrefix(line, "#") ||
			strings.HasPrefix(line, "if ") || strings.HasPrefix(line, "fi") ||
			strings.HasPrefix(line, "export") || strings.HasPrefix(line, "mkdir") ||
			strings.HasPrefix(line, "log()") || strings.HasPrefix(line, "local ") ||
			strings.ContainsAny(line, "(){") {
			continue
		}

		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		// Strip surrounding quotes
		val = strings.Trim(val, `"'`)

		c.apply(key, val)
	}
	return c, scanner.Err()
}

// apply maps a single key/value pair onto Config.
func (c *Config) apply(key, val string) {
	b := func(s string) bool { return s == "true" }
	n := func(s string, def int) int {
		if v, err := strconv.Atoi(s); err == nil {
			return v
		}
		return def
	}

	switch key {
	case "ssh_host":
		c.SSHHost = val
	case "ssh_port":
		c.SSHPort = n(val, 22)
	case "ssh_user":
		c.SSHUser = val
	case "ssh_password":
		c.SSHPassword = val
	case "ssh_mode":
		c.SSHMode = val
	case "ssh_sni_host":
		c.SSHSNIHost = val
	case "http_proxy_host":
		c.HTTPProxyHost = val
	case "http_proxy_port":
		c.HTTPProxyPort = n(val, 3128)
	case "payload_enabled":
		c.PayloadEnabled = b(val)
	case "payload":
		c.Payload = val
	case "network_mode":
		c.NetworkMode = val
	case "socks_port":
		c.SocksPort = n(val, 1080)
	case "tproxy_port":
		c.TProxyPort = n(val, 9898)
	case "redir_port":
		c.RedirPort = n(val, 9797)
	case "tun_device":
		c.TunDevice = val
	case "udpgw_server":
		c.UDPGWServer = val
	case "proxy_tcp":
		c.ProxyTCP = b(val)
	case "proxy_udp":
		c.ProxyUDP = b(val)
	case "quic":
		c.QUIC = val
	case "dns_hijack_tcp":
		c.DNSHijackTCP = b(val)
	case "dns_hijack_udp":
		c.DNSHijackUDP = b(val)
	case "dns_hijack_mode":
		c.DNSHijackMode = val
	case "channel_pool":
		c.ChannelPool = b(val)
	case "channel_pool_size":
		c.ChannelPoolSize = n(val, 8)
	case "bbr_enabled":
		c.BBREnabled = b(val)
	case "tcp_buffer_tuning":
		c.TCPBufferTuning = b(val)
	case "ipv6":
		c.IPv6 = b(val)
	case "box_dir":
		c.BoxDir = val
	case "box_run":
		c.BoxRun = val
	case "box_log":
		c.BoxLog = val
	case "box_pid":
		c.BoxPID = val
	case "bin_dir":
		c.BinDir = val
	}
}

// Get returns a snapshot of the current config (safe for concurrent reads).
func (c *Config) Get() Config {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return *c
}
