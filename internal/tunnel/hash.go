package tunnel

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode"

	"github.com/NOVA-X-PANEL/NORA-TUNNEL/internal/config"
)

const HashPrefix = "NORA://"

type ConnectionPayload struct {
	Version     int      `json:"v"`
	Core        string   `json:"core,omitempty"`   // "backhaul" or "shadow"
	Mode        string   `json:"mode"`             // "reverse" or "direct"
	Transport   string   `json:"transport"`    // "tcpmux", "tcp", "ws", "wss"
	ControlPort int      `json:"control_port"` // e.g. 3080
	IranIP      string   `json:"iran_ip"`
	KharejIP    string   `json:"kharej_ip,omitempty"`
	Token       string   `json:"token"`
	Ports       []string `json:"ports"`
	SyncSecret  string   `json:"sync_secret"`
}

func GenerateHash(t config.TunnelConfig) (string, error) {
	if t.Mode == "" {
		t.Mode = "reverse"
	}
	if t.Transport == "" {
		t.Transport = "udp+tcp"
	}
	if t.ControlPort == 0 {
		t.ControlPort = 3080
	}
	core := t.Core
	if core == "" {
		core = "backhaul"
	}

	payload := ConnectionPayload{
		Version:     1,
		Core:        core,
		Mode:        t.Mode,
		Transport:   t.Transport,
		ControlPort: t.ControlPort,
		IranIP:      strings.TrimSpace(t.IranIP),
		KharejIP:    strings.TrimSpace(t.KharejIP),
		Token:       strings.TrimSpace(t.Token),
		Ports:       t.Ports,
		SyncSecret:  t.PeerSyncSecret,
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	encoded := base64.RawURLEncoding.EncodeToString(raw)
	return HashPrefix + encoded, nil
}

// CleanHashInput strips surrounding quotes, prefixes (case-insensitively), zero-width characters,
// and any internal whitespace (spaces, tabs, newlines) introduced by mobile copy-paste or textareas.
func CleanHashInput(hashStr string) string {
	s := hashStr

	// Strip common zero-width / invisible characters FIRST
	s = strings.ReplaceAll(s, "\u200B", "") // Zero-Width Space
	s = strings.ReplaceAll(s, "\u200C", "") // Zero-Width Non-Joiner (ZWNJ / نیم‌فاصله)
	s = strings.ReplaceAll(s, "\u200D", "") // Zero-Width Joiner (ZWJ)
	s = strings.ReplaceAll(s, "\uFEFF", "") // Byte Order Mark (BOM)
	s = strings.ReplaceAll(s, "\u00A0", "") // Non-Breaking Space

	// Trim quotes, whitespace, markdown formatting
	s = strings.Trim(s, "\"'` 	\r\n")

	// Strip scheme prefix case-insensitively: NORA://, nora://, Nora://, nora:, etc.
	lower := strings.ToLower(s)
	if strings.HasPrefix(lower, "nora://") {
		s = s[7:]
	} else if strings.HasPrefix(lower, "nora:") {
		s = s[5:]
	}

	// Remove ALL internal whitespace (newlines, carriage returns, spaces, tabs)
	var sb strings.Builder
	sb.Grow(len(s))
	for _, r := range s {
		if !unicode.IsSpace(r) {
			sb.WriteRune(r)
		}
	}
	s = sb.String()

	return strings.Trim(s, "\"'` 	\r\n")
}

// DecodeBase64Robust attempts decoding across all standard and URL-safe base64 encodings,
// with or without padding, and with URL-encoded sequences unescaped.
func DecodeBase64Robust(s string) ([]byte, error) {
	if s == "" {
		return nil, errors.New("empty hash payload")
	}

	// 1. Unescape URL percent-encoding if present (e.g. %2B, %2F, %3D)
	if unescaped, err := url.QueryUnescape(s); err == nil && unescaped != "" {
		s = unescaped
	}

	// Unpadded version
	unpadded := strings.TrimRight(s, "=")

	// Try all combinations of URL-safe vs Standard, Padded vs Unpadded:
	// a. RawURLEncoding (no =)
	if raw, err := base64.RawURLEncoding.DecodeString(unpadded); err == nil {
		return raw, nil
	}

	// b. RawStdEncoding (no =)
	if raw, err := base64.RawStdEncoding.DecodeString(unpadded); err == nil {
		return raw, nil
	}

	// c. Normalized standard characters (- to +, _ to /)
	stdChars := strings.NewReplacer("-", "+", "_", "/").Replace(unpadded)
	if raw, err := base64.RawStdEncoding.DecodeString(stdChars); err == nil {
		return raw, nil
	}

	// d. Standard padding
	paddedStd := stdChars
	switch len(paddedStd) % 4 {
	case 2:
		paddedStd += "=="
	case 3:
		paddedStd += "="
	}
	if raw, err := base64.StdEncoding.DecodeString(paddedStd); err == nil {
		return raw, nil
	}

	// e. URLEncoding with padding
	paddedURL := unpadded
	switch len(paddedURL) % 4 {
	case 2:
		paddedURL += "=="
	case 3:
		paddedURL += "="
	}
	if raw, err := base64.URLEncoding.DecodeString(paddedURL); err == nil {
		return raw, nil
	}

	return nil, errors.New("invalid base64 encoding in NORA hash")
}

func ParseHash(hashStr string) (*ConnectionPayload, error) {
	cleaned := CleanHashInput(hashStr)
	if cleaned == "" {
		return nil, errors.New("empty NORA hash")
	}

	raw, err := DecodeBase64Robust(cleaned)
	if err != nil {
		return nil, errors.New("invalid base64 encoding in NORA hash")
	}

	var payload ConnectionPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("invalid payload format in NORA hash: %w", err)
	}

	if payload.Mode != "reverse" && payload.Mode != "direct" {
		return nil, errors.New("invalid mode in hash (must be 'reverse' or 'direct')")
	}
	if payload.ControlPort <= 0 || payload.ControlPort > 65535 {
		payload.ControlPort = 3080
	}
	if payload.Transport == "" {
		payload.Transport = "udp+tcp"
	}
	if payload.Core == "" {
		payload.Core = "backhaul"
	}

	return &payload, nil
}
