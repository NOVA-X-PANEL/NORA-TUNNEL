package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type TunnelConfig struct {
	Enabled        bool     `json:"enabled"`
	Core           string   `json:"core"`           // "backhaul" or "shadow" (default "backhaul")
	Mode           string   `json:"mode"`           // "reverse" or "direct"
	Role           string   `json:"role"`           // "kharej" or "iran"
	Transport      string   `json:"transport"`      // "udp+tcp", "tcp", "tcpmux", "ws", "wsmux"
	ControlPort    int      `json:"control_port"`   // default 3080
	IranIP         string   `json:"iran_ip"`
	KharejIP       string   `json:"kharej_ip"`
	Token          string   `json:"token"`
	Ports          []string `json:"ports"`          // e.g. ["443", "2053", "2083", "8080"]
	ConnectionHash string   `json:"connection_hash"`
	PeerSyncSecret string   `json:"sync_secret"`
}

type AppConfig struct {
	WebPort      int          `json:"web_port"`
	Username     string       `json:"username"`
	Password     string       `json:"password"`
	SecretKey    string       `json:"secret_key"`
	Tunnel       TunnelConfig `json:"tunnel"`
	ConfigPath   string       `json:"-"`
	mu           sync.RWMutex `json:"-"`
}

type Manager struct {
	appCfg *AppConfig
	mu     sync.RWMutex
}

var (
	GlobalConfig *AppConfig
)

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func GenerateRandomSecret(n int) string {
	return randomHex(n)
}

func DefaultConfig() *AppConfig {
	return &AppConfig{
		WebPort:   7777,
		Username:  "admin",
		Password:  "admin",
		SecretKey: randomHex(16),
		Tunnel: TunnelConfig{
			Enabled:        false,
			Core:           "backhaul",
			Mode:           "reverse",
			Role:           "none",
			Transport:      "udp+tcp",
			ControlPort:    3080,
			Token:          randomHex(12),
			Ports:          []string{},
			PeerSyncSecret: randomHex(16),
		},
	}
}

func LoadConfig(customPath string) (*AppConfig, error) {
	targetPath := customPath
	if targetPath == "" {
		if os.Geteuid() == 0 {
			targetPath = "/etc/nora-tunnel/config.json"
		} else {
			targetPath = "./config.json"
		}
	}

	cfg := DefaultConfig()
	cfg.ConfigPath = targetPath

	data, err := os.ReadFile(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			_ = cfg.Save()
			GlobalConfig = cfg
			return cfg, nil
		}
		return nil, err
	}

	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("invalid json in config: %w", err)
	}

	if cfg.WebPort == 0 {
		cfg.WebPort = 7777
	}
	if cfg.SecretKey == "" {
		cfg.SecretKey = randomHex(16)
	}
	if cfg.Tunnel.ControlPort == 0 {
		cfg.Tunnel.ControlPort = 3080
	}
	if cfg.Tunnel.Transport == "" {
		cfg.Tunnel.Transport = "udp+tcp"
	}
	if cfg.Tunnel.Core == "" {
		cfg.Tunnel.Core = "backhaul"
	}
	if cfg.Tunnel.Ports == nil {
		cfg.Tunnel.Ports = []string{}
	}

	GlobalConfig = cfg
	return cfg, nil
}

func (c *AppConfig) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	dir := filepath.Dir(c.ConfigPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(c.ConfigPath, data, 0600)
}

func NewManager(cfg *AppConfig) *Manager {
	return &Manager{appCfg: cfg}
}

func (m *Manager) Get() TunnelConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.appCfg.Tunnel
}

func (m *Manager) GetAppConfig() *AppConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.appCfg
}

func (m *Manager) Update(fn func(*TunnelConfig)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	fn(&m.appCfg.Tunnel)
	return m.appCfg.Save()
}

func (m *Manager) UpdateAuth(username, password string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if username != "" {
		m.appCfg.Username = username
	}
	if password != "" {
		m.appCfg.Password = password
	}
	return m.appCfg.Save()
}

func (m *Manager) ResetTunnel() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.appCfg.Tunnel = TunnelConfig{
		Enabled:        false,
		Mode:           "reverse",
		Role:           "none",
		Transport:      "tcpmux",
		ControlPort:    3080,
		Token:          randomHex(12),
		Ports:          []string{},
		PeerSyncSecret: randomHex(16),
		IranIP:         "",
		KharejIP:       "",
		ConnectionHash: "",
	}
	return m.appCfg.Save()
}
