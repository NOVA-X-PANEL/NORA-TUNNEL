package shadow

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/NOVA-X-PANEL/NORA-TUNNEL/internal/backhaul"
	"github.com/NOVA-X-PANEL/NORA-TUNNEL/internal/config"
)

type Manager struct {
	mu        sync.RWMutex
	ctx       context.Context
	cancel    context.CancelFunc
	running   bool
	role      string
	mode      string
	cfg       config.TunnelConfig
	startedAt time.Time

	server *Server
	client *Client
}

func NewManager() *Manager {
	return &Manager{}
}

func (m *Manager) Start(cfg config.TunnelConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		_ = m.stopLocked()
	}

	m.cfg = cfg
	m.role = cfg.Role
	m.mode = cfg.Mode
	m.ctx, m.cancel = context.WithCancel(context.Background())
	m.startedAt = time.Now()

	ctrlPort := cfg.ControlPort
	if ctrlPort == 0 {
		ctrlPort = 3080
	}

	// Reverse mode: Kharej is the SSH Server, Iran is the SSH Client
	if cfg.Role == "kharej" {
		srv, err := StartServer(fmt.Sprintf(":%d", ctrlPort), cfg.Token, "")
		if err != nil {
			m.running = false
			return fmt.Errorf("failed to start shadow server on kharej: %w", err)
		}
		m.server = srv
		m.running = true
	} else if cfg.Role == "iran" {
		targetHost := cfg.KharejIP
		if targetHost == "" {
			targetHost = "127.0.0.1"
		}

		cli := StartClient(m.ctx, targetHost, ctrlPort, cfg.Token, cfg.Ports)
		m.client = cli
		m.running = true

		// Apply firewall accounting rules for Iran
		go backhaul.EnsureFirewallRules(cfg)
	} else {
		return fmt.Errorf("unknown role: %s", cfg.Role)
	}

	return nil
}

func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stopLocked()
}

func (m *Manager) stopLocked() error {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	if m.server != nil {
		_ = m.server.Close()
		m.server = nil
	}
	if m.client != nil {
		m.client.Close()
		m.client = nil
	}
	m.running = false
	return nil
}

func (m *Manager) Restart(cfg config.TunnelConfig) error {
	_ = m.Stop()
	time.Sleep(200 * time.Millisecond)
	return m.Start(cfg)
}

func (m *Manager) GetStatus() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ctrlPort := m.cfg.ControlPort
	if ctrlPort == 0 {
		ctrlPort = 3080
	}

	st := Status{
		Core:        "shadow",
		Installed:   true,
		Running:     m.running,
		Role:        m.role,
		Mode:        m.mode,
		Transport:   "ssh-shadow",
		ControlPort: ctrlPort,
		Ports:       m.cfg.Ports,
		StartedAt:   m.startedAt,
	}

	if m.running {
		st.UptimeSec = int64(time.Since(m.startedAt).Seconds())
	}

	if m.role == "kharej" {
		st.RemoteIP = m.cfg.IranIP
		if m.server != nil {
			st.Running = true
		}
	} else if m.role == "iran" {
		st.RemoteIP = m.cfg.KharejIP
		if m.client != nil {
			st.LastPingMs = m.client.LastPing()
			st.LastError = m.client.LastErr()
			st.Running = m.client.IsConnected()
		}
	}

	return st
}
