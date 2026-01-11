package config

import (
	"net"
	"os"
	"sync"

	"github.com/danthegoodman1/specificproxy/gologger"
	"github.com/goccy/go-yaml"
)

var logger = gologger.NewLogger()

// Config holds the application configuration
type Config struct {
	// AllowedInterfaces is the list of network interface names that can be used for egress
	AllowedInterfaces []string `yaml:"allowed_interfaces"`
}

var (
	globalConfig *Config
	configMu     sync.RWMutex
)

// LoadConfig reads and parses the config file
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	configMu.Lock()
	globalConfig = &cfg
	configMu.Unlock()

	logger.Info().Strs("allowed_interfaces", cfg.AllowedInterfaces).Msg("loaded config")

	return &cfg, nil
}

// GetConfig returns the current global config
func GetConfig() *Config {
	configMu.RLock()
	defer configMu.RUnlock()
	return globalConfig
}

// IPInfo represents an available IP address
type IPInfo struct {
	Interface string `json:"interface"`
	IP        string `json:"ip"`
	Version   int    `json:"version"` // 4 or 6
}

// GetAvailableIPs returns all non-link-local IPs from allowed interfaces
func (c *Config) GetAvailableIPs() ([]IPInfo, error) {
	var result []IPInfo

	allowedSet := make(map[string]bool)
	for _, iface := range c.AllowedInterfaces {
		allowedSet[iface] = true
	}

	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	for _, iface := range interfaces {
		// Skip if not in allowed list
		if !allowedSet[iface.Name] {
			continue
		}

		// Skip interfaces that are down
		if iface.Flags&net.FlagUp == 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			logger.Warn().Err(err).Str("interface", iface.Name).Msg("failed to get addresses for interface")
			continue
		}

		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			default:
				continue
			}

			// Skip link-local addresses
			if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
				continue
			}

			// Skip loopback
			if ip.IsLoopback() {
				continue
			}

			version := 4
			if ip.To4() == nil {
				version = 6
			}

			result = append(result, IPInfo{
				Interface: iface.Name,
				IP:        ip.String(),
				Version:   version,
			})
		}
	}

	return result, nil
}

// IsIPAllowed checks if the given IP belongs to an allowed interface
func (c *Config) IsIPAllowed(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}

	allowedSet := make(map[string]bool)
	for _, iface := range c.AllowedInterfaces {
		allowedSet[iface] = true
	}

	interfaces, err := net.Interfaces()
	if err != nil {
		return false
	}

	for _, iface := range interfaces {
		if !allowedSet[iface.Name] {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			var ifaceIP net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ifaceIP = v.IP
			case *net.IPAddr:
				ifaceIP = v.IP
			default:
				continue
			}

			if ifaceIP.Equal(ip) {
				return true
			}
		}
	}

	return false
}
