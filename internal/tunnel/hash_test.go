package tunnel

import (
	"strings"
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

func TestHash_CaseInsensitivePrefix(t *testing.T) {
	cfg := config.TunnelConfig{
		Mode:        "reverse",
		Transport:   "udp+tcp",
		ControlPort: 3080,
		IranIP:      "85.9.106.284",
		KharejIP:    "91.107.165.105",
		Token:       "token-abc-123",
		Ports:       []string{"443"},
	}

	hash, err := GenerateHash(cfg)
	if err != nil {
		t.Fatalf("GenerateHash failed: %v", err)
	}

	// 1. Lowercase prefix test (as happens on mobile)
	lowerHash := strings.ToLower(hash[:7]) + hash[7:]
	p1, err := ParseHash(lowerHash)
	if err != nil {
		t.Fatalf("ParseHash with lowercase prefix failed: %v", err)
	}
	if p1.IranIP != cfg.IranIP {
		t.Errorf("expected IranIP %s, got %s", cfg.IranIP, p1.IranIP)
	}

	// 2. Mixed-case prefix
	mixedHash := "Nora://" + hash[7:]
	if _, err := ParseHash(mixedHash); err != nil {
		t.Errorf("ParseHash with Nora:// failed: %v", err)
	}

	// 3. Scheme with no slashes
	colonHash := "nora:" + hash[7:]
	if _, err := ParseHash(colonHash); err != nil {
		t.Errorf("ParseHash with nora: failed: %v", err)
	}

	// 4. Raw base64 with no scheme at all
	rawHash := hash[7:]
	if _, err := ParseHash(rawHash); err != nil {
		t.Errorf("ParseHash with raw base64 failed: %v", err)
	}

	// 5. Embedded newlines & carriage returns (iOS textarea / clipboard artifact)
	b64 := hash[7:]
	brokenHash := "nora://" + b64[:20] + "\r\n" + b64[20:40] + "\n" + b64[40:]
	if _, err := ParseHash(brokenHash); err != nil {
		t.Errorf("ParseHash with embedded newlines failed: %v", err)
	}

	// 6. Zero-width spaces & quotes
	dirtyHash := "\" \u200B nora://" + b64[:30] + "\u200C " + b64[30:] + " \uFEFF \" "
	if _, err := ParseHash(dirtyHash); err != nil {
		t.Errorf("ParseHash with zero-width characters and quotes failed: %v", err)
	}

	// 7. Base64 with padding '=' added
	paddedHash := "nora://" + b64 + "=="
	if _, err := ParseHash(paddedHash); err != nil {
		t.Errorf("ParseHash with padding added failed: %v", err)
	}
}

func TestHash_ShadowCore(t *testing.T) {
	cfg := config.TunnelConfig{
		Core:           "shadow",
		Mode:           "reverse",
		ControlPort:    3080,
		IranIP:         "37.202.244.172",
		KharejIP:       "199.247.17.46",
		Token:          "shadow-token-xyz",
		Ports:          []string{"443", "8443"},
		PeerSyncSecret: "secret-xyz",
	}

	hash, err := GenerateHash(cfg)
	if err != nil {
		t.Fatalf("GenerateHash failed: %v", err)
	}

	payload, err := ParseHash(hash)
	if err != nil {
		t.Fatalf("ParseHash failed: %v", err)
	}

	if payload.Core != "shadow" {
		t.Errorf("Expected Core 'shadow', got '%s'", payload.Core)
	}
	if payload.Token != "shadow-token-xyz" {
		t.Errorf("Expected token 'shadow-token-xyz', got '%s'", payload.Token)
	}
}
