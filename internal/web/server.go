package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/NOVA-X-PANEL/NORA-TUNNEL/internal/backhaul"
	"github.com/NOVA-X-PANEL/NORA-TUNNEL/internal/config"
	"github.com/NOVA-X-PANEL/NORA-TUNNEL/internal/tunnel"
)

//go:embed all:assets
var webFS embed.FS

type Server struct {
	cfgMgr  *config.Manager
	bhMgr   *backhaul.Manager
	mux     *http.ServeMux
	version string
}

type ApiResponse struct {
	Success bool        `json:"success"`
	Msg     string      `json:"msg,omitempty"`
	Obj     interface{} `json:"obj,omitempty"`
}

func NewServer(cfgMgr *config.Manager, bhMgr *backhaul.Manager, version string) *Server {
	s := &Server{
		cfgMgr:  cfgMgr,
		bhMgr:   bhMgr,
		mux:     http.NewServeMux(),
		version: version,
	}
	s.registerRoutes()
	return s
}

func (s *Server) registerRoutes() {
	// API routes
	s.mux.HandleFunc("/api/status", s.handleStatus)
	s.mux.HandleFunc("/api/tunnel/create", s.handleCreateTunnel)
	s.mux.HandleFunc("/api/tunnel/connect", s.handleConnectTunnel)
	s.mux.HandleFunc("/api/tunnel/restart", s.handleRestartTunnel)
	s.mux.HandleFunc("/api/tunnel/stop", s.handleStopTunnel)
	s.mux.HandleFunc("/api/ports/add", s.handleAddPort)
	s.mux.HandleFunc("/api/ports/delete", s.handleDeletePort)
	s.mux.HandleFunc("/api/ping", s.handlePing)
	s.mux.HandleFunc("/api/logs", s.handleLogs)
	s.mux.HandleFunc("/api/settings/password", s.handleUpdatePassword)

	// Static web files
	subFS, err := fs.Sub(webFS, "web")
	if err == nil {
		fileServer := http.FileServer(http.FS(subFS))
		s.mux.Handle("/", fileServer)
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Enable CORS for development/cross-access
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	s.mux.ServeHTTP(w, r)
}

func (s *Server) jsonResponse(w http.ResponseWriter, success bool, msg string, obj interface{}) {
	w.Header().Set("Content-Type", "application/json")
	resp := ApiResponse{
		Success: success,
		Msg:     msg,
		Obj:     obj,
	}
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	cfg := s.cfgMgr.Get()
	status := s.bhMgr.GetStatus()

	data := map[string]interface{}{
		"version": s.version,
		"config":  cfg,
		"status":  status,
	}
	s.jsonResponse(w, true, "", data)
}

func (s *Server) handleCreateTunnel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		IranIP      string   `json:"iran_ip"`
		Mode        string   `json:"mode"`
		Transport   string   `json:"transport"`
		ControlPort int      `json:"control_port"`
		Ports       []string `json:"ports"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonResponse(w, false, "Invalid JSON: "+err.Error(), nil)
		return
	}

	if req.IranIP == "" {
		s.jsonResponse(w, false, "Iran IP is required", nil)
		return
	}

	if req.Mode == "" {
		req.Mode = "reverse"
	}
	if req.Transport == "" {
		req.Transport = "tcpmux"
	}
	if req.ControlPort <= 0 {
		req.ControlPort = 3080
	}
	if len(req.Ports) == 0 {
		req.Ports = []string{"443", "2053", "2083", "8080"}
	}

	// Update configuration
	cfg := s.cfgMgr.Get()
	cfg.Role = "kharej"
	cfg.Mode = req.Mode
	cfg.Transport = req.Transport
	cfg.ControlPort = req.ControlPort
	cfg.IranIP = req.IranIP
	cfg.Ports = req.Ports
	if cfg.Token == "" {
		cfg.Token = config.GenerateRandomSecret(16)
	}
	if cfg.PeerSyncSecret == "" {
		cfg.PeerSyncSecret = config.GenerateRandomSecret(16)
	}

	// Generate Hash
	hash, err := tunnel.GenerateHash(cfg)
	if err != nil {
		s.jsonResponse(w, false, "Failed to generate hash: "+err.Error(), nil)
		return
	}
	cfg.ConnectionHash = hash

	if err := s.cfgMgr.Update(func(c *config.TunnelConfig) {
		*c = cfg
	}); err != nil {
		s.jsonResponse(w, false, "Failed to save config: "+err.Error(), nil)
		return
	}

	// Start or restart Backhaul on Kharej
	if err := s.bhMgr.Restart(cfg); err != nil {
		s.jsonResponse(w, false, "Tunnel generated but failed to start Backhaul: "+err.Error(), map[string]interface{}{
			"hash": hash,
		})
		return
	}

	s.jsonResponse(w, true, "Tunnel generated successfully", map[string]interface{}{
		"hash": hash,
	})
}

func (s *Server) handleConnectTunnel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Hash string `json:"hash"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonResponse(w, false, "Invalid JSON: "+err.Error(), nil)
		return
	}

	payload, err := tunnel.ParseHash(req.Hash)
	if err != nil {
		s.jsonResponse(w, false, "Invalid hash format: "+err.Error(), nil)
		return
	}

	cfg := s.cfgMgr.Get()
	cfg.Role = "iran"
	cfg.Mode = payload.Mode
	cfg.Transport = payload.Transport
	cfg.ControlPort = payload.ControlPort
	cfg.IranIP = payload.IranIP
	cfg.KharejIP = payload.KharejIP
	cfg.Token = payload.Token
	cfg.Ports = payload.Ports
	cfg.PeerSyncSecret = payload.SyncSecret
	cfg.ConnectionHash = req.Hash

	if err := s.cfgMgr.Update(func(c *config.TunnelConfig) {
		*c = cfg
	}); err != nil {
		s.jsonResponse(w, false, "Failed to save config: "+err.Error(), nil)
		return
	}

	// Start Backhaul on Iran
	if err := s.bhMgr.Restart(cfg); err != nil {
		s.jsonResponse(w, false, "Config applied but failed to start Backhaul: "+err.Error(), nil)
		return
	}

	s.jsonResponse(w, true, "Tunnel connected successfully via hash", nil)
}

func (s *Server) handleRestartTunnel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	cfg := s.cfgMgr.Get()
	if err := s.bhMgr.Restart(cfg); err != nil {
		s.jsonResponse(w, false, "Failed to restart: "+err.Error(), nil)
		return
	}

	s.jsonResponse(w, true, "Tunnel restarted successfully", nil)
}

func (s *Server) handleStopTunnel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := s.bhMgr.Stop(); err != nil {
		s.jsonResponse(w, false, "Failed to stop: "+err.Error(), nil)
		return
	}

	s.jsonResponse(w, true, "Tunnel stopped successfully", nil)
}

func (s *Server) handleAddPort(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Port string `json:"port"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonResponse(w, false, "Invalid JSON: "+err.Error(), nil)
		return
	}

	req.Port = strings.TrimSpace(req.Port)
	if req.Port == "" {
		s.jsonResponse(w, false, "Port cannot be empty", nil)
		return
	}

	cfg := s.cfgMgr.Get()

	// Check if port already exists
	for _, p := range cfg.Ports {
		if p == req.Port {
			s.jsonResponse(w, true, "Port already exists", nil)
			return
		}
	}

	cfg.Ports = append(cfg.Ports, req.Port)

	// Update hash if Kharej
	if cfg.Role == "kharej" {
		if h, err := tunnel.GenerateHash(cfg); err == nil {
			cfg.ConnectionHash = h
		}
	}

	if err := s.cfgMgr.Update(func(c *config.TunnelConfig) {
		*c = cfg
	}); err != nil {
		s.jsonResponse(w, false, "Failed to save: "+err.Error(), nil)
		return
	}

	// Reload Backhaul with new port list
	_ = s.bhMgr.UpdatePorts(cfg)

	s.jsonResponse(w, true, "Port added successfully", map[string]interface{}{
		"ports": cfg.Ports,
		"hash":  cfg.ConnectionHash,
	})
}

func (s *Server) handleDeletePort(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Port string `json:"port"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonResponse(w, false, "Invalid JSON: "+err.Error(), nil)
		return
	}

	cfg := s.cfgMgr.Get()
	var newPorts []string
	found := false
	for _, p := range cfg.Ports {
		if p == req.Port {
			found = true
			continue
		}
		newPorts = append(newPorts, p)
	}

	if !found {
		s.jsonResponse(w, false, "Port not found in active list", nil)
		return
	}

	cfg.Ports = newPorts
	if cfg.Role == "kharej" {
		if h, err := tunnel.GenerateHash(cfg); err == nil {
			cfg.ConnectionHash = h
		}
	}

	if err := s.cfgMgr.Update(func(c *config.TunnelConfig) {
		*c = cfg
	}); err != nil {
		s.jsonResponse(w, false, "Failed to save: "+err.Error(), nil)
		return
	}

	_ = s.bhMgr.UpdatePorts(cfg)

	s.jsonResponse(w, true, "Port removed successfully", map[string]interface{}{
		"ports": cfg.Ports,
	})
}

func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfgMgr.Get()
	targetIP := cfg.IranIP
	if cfg.Role == "iran" {
		targetIP = cfg.KharejIP
	}

	if targetIP == "" {
		s.jsonResponse(w, false, "Target peer IP not configured yet", nil)
		return
	}

	start := time.Now()
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", targetIP, cfg.ControlPort), 3*time.Second)
	if err != nil {
		// Try pinging ICMP or fallback
		s.jsonResponse(w, false, "Could not reach peer control port: "+err.Error(), nil)
		return
	}
	conn.Close()
	latencyMs := int64(time.Since(start).Milliseconds())

	s.bhMgr.UpdatePing(latencyMs)

	s.jsonResponse(w, true, "Ping success", map[string]interface{}{
		"latency_ms": latencyMs,
		"target":     targetIP,
	})
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	logs := s.bhMgr.GetRecentLogs(100)
	s.jsonResponse(w, true, "", logs)
}

func (s *Server) handleUpdatePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonResponse(w, false, "Invalid JSON: "+err.Error(), nil)
		return
	}

	if err := s.cfgMgr.UpdateAuth(req.Username, req.Password); err != nil {
		s.jsonResponse(w, false, "Failed to update: "+err.Error(), nil)
		return
	}

	s.jsonResponse(w, true, "Credentials updated successfully", nil)
}
