package tunnel

import (
	"testing"

	"github.com/NOVA-X-PANEL/NORA-TUNNEL/internal/config"
)

func TestHash_UDPTCPTransport(t *testing.T) {
	cfg := config.TunnelConfig{
		Mode:           "reverse",
		Transport:      "udp+tcp",
		ControlPort:    3080,
		IranIP:         "37.202.244.172",
		KharejIP:       "199.247.17.46",
		Token:          "token-abc-123",
		Ports:          []string{"8080", "2087"},
		PeerSyncSecret: "secret-123",
	}

	hash, err := GenerateHash(cfg)
	if err != nil {
		t.Fatalf("GenerateHash failed: %v", err)
	}

	payload, err := ParseHash(hash)
	if err != nil {
		t.Fatalf("ParseHash failed: %v", err)
	}

	if payload.Transport != "udp+tcp" {
		t.Errorf("Expected Transport 'udp+tcp', got '%s'", payload.Transport)
	}
	if payload.Mode != "reverse" {
		t.Errorf("Expected Mode 'reverse', got '%s'", payload.Mode)
	}
	if len(payload.Ports) != 2 || payload.Ports[0] != "8080" || payload.Ports[1] != "2087" {
		t.Errorf("Expected ports 8080 and 2087, got %v", payload.Ports)
	}
}
