package tunnel

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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

func ParseHash(hashStr string) (*ConnectionPayload, error) {
	trimmed := strings.TrimSpace(hashStr)
	trimmed = strings.TrimPrefix(trimmed, HashPrefix)

	raw, err := base64.RawURLEncoding.DecodeString(trimmed)
	if err != nil {
		// Fallback to standard base64 if needed
		raw, err = base64.StdEncoding.DecodeString(trimmed)
		if err != nil {
			return nil, errors.New("invalid base64 encoding in NORA hash")
		}
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
