package shadow

import (
	"time"
)

type Status struct {
	Core        string    `json:"core"` // "shadow"
	Installed   bool      `json:"installed"`
	Running     bool      `json:"running"`
	Role        string    `json:"role"`
	Mode        string    `json:"mode"`
	Transport   string    `json:"transport"`
	ControlPort int       `json:"control_port"`
	RemoteIP    string    `json:"remote_ip"`
	Ports       []string  `json:"ports"`
	UptimeSec   int64     `json:"uptime_sec"`
	LastPingMs  int       `json:"last_ping_ms"`
	StartedAt   time.Time `json:"started_at"`
	LastError   string    `json:"last_error"`
}
