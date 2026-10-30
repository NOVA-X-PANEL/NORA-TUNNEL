package web

import (
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
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
	// Public installation endpoints
	s.mux.HandleFunc("/install.sh", s.handleInstallScript)
	s.mux.HandleFunc("/download/", s.handleDownload)

	// API routes
	s.mux.HandleFunc("/api/auth/login", s.handleLogin)
	s.mux.HandleFunc("/api/auth/logout", s.handleLogout)
	s.mux.HandleFunc("/api/auth/check", s.handleAuthCheck)
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
	subFS, err := fs.Sub(webFS, "assets")
	if err == nil {
		fileServer := http.FileServer(http.FS(subFS))
		s.mux.Handle("/", fileServer)
	}
}

func (s *Server) handleInstallScript(w http.ResponseWriter, r *http.Request) {
	data, err := fs.ReadFile(webFS, "assets/install.sh")
	if err != nil {
		http.Error(w, "install.sh not found", http.StatusNotFound)
		return
	}

	host := r.Host
	if host == "" {
		host = "91.107.165.105:7777"
	}

	content := strings.Replace(string(data), `NORA_MIRROR=""`, fmt.Sprintf(`NORA_MIRROR="%s"`, host), 1)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(content))
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	filename := filepath.Base(r.URL.Path)
	switch filename {
	case "nora-tunnel-linux-amd64", "nora-tunnel":
		exePath, err := os.Executable()
		if err == nil {
			http.ServeFile(w, r, exePath)
			return
		}
		http.ServeFile(w, r, "/usr/local/bin/nora-tunnel")
	case "backhaul", "backhaul-linux-amd64":
		candidates := []string{"/usr/local/bin/backhaul", "/opt/nora-tunnel/bin/backhaul", "./backhaul"}
		for _, c := range candidates {
			if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
				http.ServeFile(w, r, c)
				return
			}
		}
		http.NotFound(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) generateSessionToken() string {
	appCfg := s.cfgMgr.GetAppConfig()
	data := fmt.Sprintf("%s:%s:%s", appCfg.Username, appCfg.Password, appCfg.SecretKey)
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}

func (s *Server) isAuthenticated(r *http.Request) bool {
	expectedToken := s.generateSessionToken()

	// 1. Check Cookie
	if c, err := r.Cookie("nora_session"); err == nil && c.Value == expectedToken {
		return true
	}

	// 2. Check Authorization Header
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if token == expectedToken {
			return true
		}
	} else if strings.HasPrefix(authHeader, "Basic ") {
		raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(authHeader, "Basic "))
		if err == nil {
			parts := strings.SplitN(string(raw), ":", 2)
			appCfg := s.cfgMgr.GetAppConfig()
			if len(parts) == 2 && parts[0] == appCfg.Username && parts[1] == appCfg.Password {
				return true
			}
		}
	}

	return false
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonResponse(w, false, "Invalid JSON", nil)
		return
	}

	appCfg := s.cfgMgr.GetAppConfig()
	if req.Username != appCfg.Username || req.Password != appCfg.Password {
		s.jsonResponse(w, false, "نام کاربری یا رمز عبور اشتباه است", nil)
		return
	}

	token := s.generateSessionToken()
	http.SetCookie(w, &http.Cookie{
		Name:     "nora_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		MaxAge:   86400 * 30, // 30 days
		SameSite: http.SameSiteLaxMode,
	})

	s.jsonResponse(w, true, "ورود موفقیت‌آمیز بود", map[string]string{
		"token": token,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     "nora_session",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})
	s.jsonResponse(w, true, "خروج انجام شد", nil)
}

func (s *Server) handleAuthCheck(w http.ResponseWriter, r *http.Request) {
	s.jsonResponse(w, s.isAuthenticated(r), "", nil)
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

	// Protect all /api/ endpoints except /api/auth/login and /api/auth/check
	if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/auth/login" && r.URL.Path != "/api/auth/check" {
		if !s.isAuthenticated(r) {
			w.WriteHeader(http.StatusUnauthorized)
			s.jsonResponse(w, false, "Unauthorized", nil)
			return
		}
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
