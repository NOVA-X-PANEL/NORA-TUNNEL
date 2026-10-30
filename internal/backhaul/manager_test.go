package backhaul

import (
	"strings"
	"testing"

	"github.com/NOVA-X-PANEL/NORA-TUNNEL/internal/config"
)

func TestGenerateConfig_UDPTCPTransport(t *testing.T) {
	mgr := NewManager()
	mgr.configFile = "/tmp/test_config.toml"

	// Test udp+tcp mapping on server (reverse mode on Iran)
	cfg := config.TunnelConfig{
		Enabled:     true,
		Mode:        "reverse",
		Role:        "iran",
		Transport:   "udp+tcp",
		ControlPort: 3080,
		Token:       "test-token",
		Ports:       []string{"8080", "2087"},
	}

	conf, err := mgr.GenerateConfig(cfg)
	if err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
	}

	if !strings.Contains(conf, `transport = "tcp"`) {
		t.Errorf("Expected transport = \"tcp\", got:\n%s", conf)
	}

	if !strings.Contains(conf, `accept_udp = true`) {
		t.Errorf("Expected accept_udp = true, got:\n%s", conf)
	}

	if !strings.Contains(conf, `"8080"`) || !strings.Contains(conf, `"2087"`) {
		t.Errorf("Expected ports 8080 and 2087, got:\n%s", conf)
	}

	// Test client (reverse mode on Kharej)
	cfgKharej := config.TunnelConfig{
		Enabled:     true,
		Mode:        "reverse",
		Role:        "kharej",
		Transport:   "udp+tcp",
		IranIP:      "1.2.3.4",
		ControlPort: 3080,
		Token:       "test-token",
		Ports:       []string{"8080", "2087"},
	}

	confKharej, err := mgr.GenerateConfig(cfgKharej)
	if err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
	}

	if !strings.Contains(confKharej, `transport = "tcp"`) {
		t.Errorf("Expected transport = \"tcp\" on client, got:\n%s", confKharej)
	}

	if !strings.Contains(confKharej, `remote_addr = "1.2.3.4:3080"`) {
		t.Errorf("Expected remote_addr, got:\n%s", confKharej)
	}
}
