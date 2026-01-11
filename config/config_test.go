package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	// Create temp config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	content := `allowed_interfaces:
  - eth0
  - eth1
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if len(cfg.AllowedInterfaces) != 2 {
		t.Errorf("expected 2 interfaces, got %d", len(cfg.AllowedInterfaces))
	}

	if cfg.AllowedInterfaces[0] != "eth0" {
		t.Errorf("expected eth0, got %s", cfg.AllowedInterfaces[0])
	}

	if cfg.AllowedInterfaces[1] != "eth1" {
		t.Errorf("expected eth1, got %s", cfg.AllowedInterfaces[1])
	}
}

func TestLoadConfig_InvalidPath(t *testing.T) {
	_, err := LoadConfig("/nonexistent/path/config.yaml")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestLoadConfig_InvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	content := `invalid: yaml: content: [[[`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadConfig(configPath)
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestGetConfig(t *testing.T) {
	// Create and load a config
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	content := `allowed_interfaces:
  - lo
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}

	// GetConfig should return the same config
	globalCfg := GetConfig()
	if globalCfg != cfg {
		t.Error("GetConfig should return the loaded config")
	}
}

func TestIPInfo(t *testing.T) {
	info := IPInfo{
		Interface: "eth0",
		IP:        "192.168.1.1",
		Version:   4,
	}

	if info.Interface != "eth0" {
		t.Errorf("expected eth0, got %s", info.Interface)
	}
	if info.Version != 4 {
		t.Errorf("expected version 4, got %d", info.Version)
	}
}

// TestIsIPAllowed tests with loopback interface which should always exist
func TestIsIPAllowed_NonExistent(t *testing.T) {
	cfg := &Config{
		AllowedInterfaces: []string{"eth0"},
	}

	// This IP definitely doesn't exist on eth0
	if cfg.IsIPAllowed("10.255.255.255") {
		t.Error("should not allow non-existent IP")
	}

	// Invalid IP format
	if cfg.IsIPAllowed("not-an-ip") {
		t.Error("should not allow invalid IP format")
	}
}

func TestIsIPAllowed_EmptyInterfaces(t *testing.T) {
	cfg := &Config{
		AllowedInterfaces: []string{},
	}

	// With no allowed interfaces, nothing should be allowed
	if cfg.IsIPAllowed("127.0.0.1") {
		t.Error("should not allow any IP with empty interface list")
	}
}
