package backhaul

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/NOVA-X-PANEL/NORA-TUNNEL/internal/config"
)

const (
	DefaultBackhaulVersion = "v0.7.2"
	SystemdServiceName     = "nora-backhaul.service"
	MaxLogLines            = 150
)

type Status struct {
	Installed   bool      `json:"installed"`
	BinaryPath  string    `json:"binary_path"`
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

type Manager struct {
	mu         sync.RWMutex
	binaryPath string
	configDir  string
	configFile string
	status     Status
	cmd        *exec.Cmd
	logBuffer  []string
	logMu      sync.RWMutex
}

var GlobalManager *Manager

func NewManager() *Manager {
	baseDir := "/etc/nora-tunnel/backhaul"
	if os.Geteuid() != 0 {
		baseDir = "./backhaul_data"
	}

	m := &Manager{
		configDir:  baseDir,
		configFile: filepath.Join(baseDir, "config.toml"),
		logBuffer:  make([]string, 0, MaxLogLines),
	}
	_ = os.MkdirAll(baseDir, 0755)

	m.detectBinary()
	GlobalManager = m
	return m
}

func findBinaryOnDisk() string {
	candidates := []string{
		"/usr/local/bin/backhaul",
		"/opt/nora-tunnel/bin/backhaul",
		"./bin/backhaul",
		"./backhaul",
	}

	if envPath, err := exec.LookPath("backhaul"); err == nil {
		candidates = append([]string{envPath}, candidates...)
	}

	for _, path := range candidates {
		if fi, err := os.Stat(path); err == nil && !fi.IsDir() && (fi.Mode()&0111 != 0) {
			return path
		}
	}
	return ""
}

func (m *Manager) detectBinary() string {
	path := findBinaryOnDisk()
	m.mu.Lock()
	defer m.mu.Unlock()

	if path != "" {
		m.binaryPath = path
		m.status.Installed = true
		m.status.BinaryPath = path
	} else {
		m.status.Installed = false
	}
	return path
}

func (m *Manager) EnsureBinary(ctx context.Context) (string, error) {
	if p := m.detectBinary(); p != "" {
		return p, nil
	}

	arch := runtime.GOARCH
	var downloadURL string
	switch arch {
	case "amd64":
		downloadURL = fmt.Sprintf("https://github.com/Musixal/Backhaul/releases/download/%s/backhaul_linux_amd64.tar.gz", DefaultBackhaulVersion)
	case "arm64":
		downloadURL = fmt.Sprintf("https://github.com/Musixal/Backhaul/releases/download/%s/backhaul_linux_arm64.tar.gz", DefaultBackhaulVersion)
	default:
		return "", fmt.Errorf("unsupported architecture for auto-download: %s", arch)
	}

	installDir := "/opt/nora-tunnel/bin"
	if os.Geteuid() != 0 {
		installDir = "./bin"
	}
	if err := os.MkdirAll(installDir, 0755); err != nil {
		return "", err
	}

	targetBin := filepath.Join(installDir, "backhaul")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to download backhaul: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status downloading backhaul: %s", resp.Status)
	}

	gzReader, err := gzip.NewReader(resp.Body)
	if err != nil {
		return "", err
	}
	defer gzReader.Close()

	tarReader := tar.NewReader(gzReader)
	found := false
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		if header.Name == "backhaul" || strings.HasSuffix(header.Name, "/backhaul") {
			outFile, err := os.OpenFile(targetBin, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0755)
			if err != nil {
				return "", err
			}
			if _, err := io.Copy(outFile, tarReader); err != nil {
				outFile.Close()
				return "", err
			}
			outFile.Close()
			found = true
			break
		}
	}

	if !found {
		return "", fmt.Errorf("backhaul binary not found in downloaded archive")
	}

	// Try symlink to /usr/local/bin if root
	if os.Geteuid() == 0 {
		_ = os.Remove("/usr/local/bin/backhaul")
		_ = os.Symlink(targetBin, "/usr/local/bin/backhaul")
	}

	return m.detectBinary(), nil
}

func (m *Manager) GenerateConfig(cfg config.TunnelConfig) (string, error) {
	if cfg.ControlPort == 0 {
		cfg.ControlPort = 3080
	}
	if cfg.Transport == "" {
		cfg.Transport = "tcpmux"
	}

	// Format port list
	formattedPorts := make([]string, 0, len(cfg.Ports))
	for _, p := range cfg.Ports {
		trimmed := strings.TrimSpace(p)
		if trimmed == "" {
			continue
		}
		if !strings.Contains(trimmed, "=") && !strings.Contains(trimmed, ":") {
			formattedPorts = append(formattedPorts, fmt.Sprintf(`"%s"`, trimmed))
		} else {
			formattedPorts = append(formattedPorts, fmt.Sprintf(`"%s"`, trimmed))
		}
	}
	portsBlock := strings.Join(formattedPorts, ",\n    ")
	if portsBlock != "" {
		portsBlock = "    " + portsBlock
	}

	var sb strings.Builder

	// Mode Logic:
	// Reverse Mode:
	// - Iran is SERVER (listening on control port & forwarded ports)
	// - Kharej is CLIENT (dialing Iran)
	// Direct Mode:
	// - Kharej is SERVER (listening on control port)
	// - Iran is CLIENT (dialing Kharej)
	isServer := false
	if cfg.Mode == "reverse" {
		if cfg.Role == "iran" {
			isServer = true
		}
	} else if cfg.Mode == "direct" {
		if cfg.Role == "kharej" {
			isServer = true
		}
	}

	if isServer {
		sb.WriteString("[server]\n")
		sb.WriteString(fmt.Sprintf("bind_addr = \"0.0.0.0:%d\"\n", cfg.ControlPort))
		sb.WriteString(fmt.Sprintf("transport = \"%s\"\n", cfg.Transport))
		sb.WriteString("accept_udp = true\n")
		sb.WriteString(fmt.Sprintf("token = \"%s\"\n", cfg.Token))
		sb.WriteString("keepalive_period = 75\n")
		sb.WriteString("nodelay = true\n")
		sb.WriteString("heartbeat = 30\n")
		sb.WriteString("channel_size = 4096\n")
		sb.WriteString("sniffer = false\n")
		sb.WriteString("web_port = 0\n")
		sb.WriteString("log_level = \"info\"\n")
		sb.WriteString("ports = [\n")
		if portsBlock != "" {
			sb.WriteString(portsBlock + "\n")
		}
		sb.WriteString("]\n")
	} else {
		// Client
		remoteIP := cfg.IranIP
		if cfg.Mode == "direct" {
			remoteIP = cfg.KharejIP
		}
		if remoteIP == "" {
			remoteIP = "127.0.0.1"
		}

		sb.WriteString("[client]\n")
		sb.WriteString(fmt.Sprintf("remote_addr = \"%s:%d\"\n", remoteIP, cfg.ControlPort))
		sb.WriteString(fmt.Sprintf("transport = \"%s\"\n", cfg.Transport))
		sb.WriteString(fmt.Sprintf("token = \"%s\"\n", cfg.Token))
		sb.WriteString("connection_pool = 10\n")
		sb.WriteString("keepalive_period = 75\n")
		sb.WriteString("nodelay = true\n")
		sb.WriteString("retry_interval = 3\n")
		sb.WriteString("dial_timeout = 10\n")
		sb.WriteString("sniffer = false\n")
		sb.WriteString("web_port = 0\n")
		sb.WriteString("log_level = \"info\"\n")
	}

	content := sb.String()
	if err := os.WriteFile(m.configFile, []byte(content), 0600); err != nil {
		return "", err
	}
	return content, nil
}

func (m *Manager) Start(cfg config.TunnelConfig) error {
	bin, err := m.EnsureBinary(context.Background())
	if err != nil {
		return fmt.Errorf("backhaul binary missing and download failed: %w", err)
	}

	EnsureFirewallRules(cfg)

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := m.GenerateConfig(cfg); err != nil {
		return fmt.Errorf("failed to write config.toml: %w", err)
	}

	// Try systemd first if running as root
	if os.Geteuid() == 0 && hasSystemd() {
		if err := m.installSystemdUnit(bin, m.configFile); err == nil {
			_ = exec.Command("systemctl", "daemon-reload").Run()
			if err := exec.Command("systemctl", "restart", SystemdServiceName).Run(); err == nil {
				_ = exec.Command("systemctl", "enable", SystemdServiceName).Run()
				m.status.Running = true
				m.status.StartedAt = time.Now()
				m.status.Role = cfg.Role
				m.status.Mode = cfg.Mode
				m.status.Transport = cfg.Transport
				m.status.ControlPort = cfg.ControlPort
				m.status.Ports = cfg.Ports
				m.appendLog(fmt.Sprintf("[NORA] Started Backhaul service via systemd (%s mode, role: %s)", cfg.Mode, cfg.Role))
				return nil
			}
		}
	}

	// Fallback to internal supervisor process
	if m.cmd != nil && m.cmd.Process != nil {
		_ = m.cmd.Process.Kill()
		_ = m.cmd.Wait()
	}

	cmd := exec.Command(bin, "-c", m.configFile)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		m.status.Running = false
		m.status.LastError = err.Error()
		return err
	}

	m.cmd = cmd
	m.status.Running = true
	m.status.StartedAt = time.Now()
	m.status.Role = cfg.Role
	m.status.Mode = cfg.Mode
	m.status.Transport = cfg.Transport
	m.status.ControlPort = cfg.ControlPort
	m.status.Ports = cfg.Ports

	go func() {
		buf := make([]byte, 2048)
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				line := string(bytes.TrimSpace(buf[:n]))
				m.appendLog(line)
			}
			if err != nil {
				break
			}
		}
		_ = cmd.Wait()
		m.mu.Lock()
		m.status.Running = false
		m.mu.Unlock()
		m.appendLog("[NORA] Backhaul process stopped.")
	}()

	m.appendLog(fmt.Sprintf("[NORA] Started Backhaul process (PID: %d)", cmd.Process.Pid))
	return nil
}

func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if os.Geteuid() == 0 && hasSystemd() {
		_ = exec.Command("systemctl", "stop", SystemdServiceName).Run()
		_ = exec.Command("systemctl", "disable", SystemdServiceName).Run()
	}

	if m.cmd != nil && m.cmd.Process != nil {
		_ = m.cmd.Process.Kill()
		_ = m.cmd.Wait()
		m.cmd = nil
	}

	m.status.Running = false
	m.appendLog("[NORA] Backhaul stopped.")
	return nil
}

func (m *Manager) Reset() error {
	_ = m.Stop()
	m.mu.Lock()
	defer m.mu.Unlock()

	_ = os.Remove(m.configFile)

	m.status.Running = false
	m.status.Role = "none"
	m.status.LastPingMs = 0
	m.status.LastError = ""
	m.status.Ports = nil
	m.status.StartedAt = time.Time{}
	m.appendLog("[NORA] Tunnel configuration removed and reset.")
	return nil
}

func (m *Manager) Restart(cfg config.TunnelConfig) error {
	_ = m.Stop()
	time.Sleep(500 * time.Millisecond)
	return m.Start(cfg)
}

func (m *Manager) GetStatus() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()

	st := m.status
	if os.Geteuid() == 0 && hasSystemd() {
		if err := exec.Command("systemctl", "is-active", "--quiet", SystemdServiceName).Run(); err == nil {
			st.Running = true
		} else {
			st.Running = false
		}
	} else if m.cmd != nil && m.cmd.Process != nil {
		if err := m.cmd.Process.Signal(os.Signal(nil)); err == nil {
			st.Running = true
		} else {
			st.Running = false
		}
	}

	if st.Running && !st.StartedAt.IsZero() {
		st.UptimeSec = int64(time.Since(st.StartedAt).Seconds())
	}
	return st
}

func (m *Manager) appendLog(line string) {
	m.logMu.Lock()
	defer m.logMu.Unlock()

	lines := strings.Split(line, "\n")
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" {
			continue
		}
		m.logBuffer = append(m.logBuffer, fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), trimmed))
		if len(m.logBuffer) > MaxLogLines {
			m.logBuffer = m.logBuffer[len(m.logBuffer)-MaxLogLines:]
		}
	}
}

func (m *Manager) GetLogs() []string {
	m.logMu.RLock()
	defer m.logMu.RUnlock()

	out := make([]string, len(m.logBuffer))
	copy(out, m.logBuffer)
	return out
}

func (m *Manager) UpdatePing(latencyMs int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.LastPingMs = int(latencyMs)
}

func (m *Manager) UpdatePorts(cfg config.TunnelConfig) error {
	// Re-write config.toml
	if _, err := m.GenerateConfig(cfg); err != nil {
		return err
	}
	// Restart Backhaul if running
	st := m.GetStatus()
	if st.Running {
		return m.Restart(cfg)
	}
	return nil
}

func (m *Manager) GetRecentLogs(n int) []string {
	logs := m.GetLogs()
	if len(logs) <= n {
		return logs
	}
	return logs[len(logs)-n:]
}

func (m *Manager) installSystemdUnit(bin, conf string) error {
	unitContent := fmt.Sprintf(`[Unit]
Description=NORA Backhaul Tunnel Service
After=network.target network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
ExecStart=%s -c %s
Restart=always
RestartSec=3s
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
`, bin, conf)

	return os.WriteFile(filepath.Join("/etc/systemd/system", SystemdServiceName), []byte(unitContent), 0644)
}

func hasSystemd() bool {
	_, err := exec.LookPath("systemctl")
	return err == nil
}

func EnsureFirewallRules(cfg config.TunnelConfig) {
	if os.Geteuid() != 0 {
		return
	}

	// 1. Ensure default policies are ACCEPT
	_ = exec.Command("iptables", "-P", "INPUT", "ACCEPT").Run()
	_ = exec.Command("iptables", "-P", "FORWARD", "ACCEPT").Run()
	_ = exec.Command("iptables", "-P", "OUTPUT", "ACCEPT").Run()

	// 2. Filter broadcast noise like MikroTik MNDP (port 5678)
	_ = exec.Command("bash", "-c", "iptables -C INPUT -p udp --dport 5678 -j DROP 2>/dev/null || iptables -I INPUT 1 -p udp --dport 5678 -j DROP").Run()

	// 3. Ensure control port is open
	if cfg.ControlPort > 0 {
		cpStr := fmt.Sprintf("%d", cfg.ControlPort)
		_ = exec.Command("iptables", "-I", "INPUT", "1", "-p", "tcp", "--dport", cpStr, "-j", "ACCEPT").Run()
		_ = exec.Command("ufw", "allow", cpStr).Run()
	}

	// 4. For each configured port, ensure open in INPUT and UFW, and remove any NAT PREROUTING hijacking
	for _, p := range cfg.Ports {
		cleanPort := strings.TrimSpace(p)
		if strings.Contains(cleanPort, "=") {
			cleanPort = strings.Split(cleanPort, "=")[0]
		}
		cleanPort = strings.TrimSpace(cleanPort)
		if cleanPort == "" {
			continue
		}

		// Ensure allowed at very top of INPUT chain
		_ = exec.Command("iptables", "-I", "INPUT", "1", "-p", "tcp", "--dport", cleanPort, "-j", "ACCEPT").Run()
		_ = exec.Command("ufw", "allow", cleanPort).Run()

		// Clean up rogue UDP 443 flood rules if TCP tunnel
		if cleanPort == "443" {
			_ = exec.Command("bash", "-c", "while iptables -D INPUT -p udp --dport 443 -j ACCEPT 2>/dev/null; do :; done").Run()
		}

		// Remove any stale NAT PREROUTING rules (like DNAT/REDIRECT) hijacking this port
		if out, err := exec.Command("iptables", "-t", "nat", "-S", "PREROUTING").Output(); err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				line = strings.TrimSpace(line)
				if (strings.Contains(line, fmt.Sprintf("--dport %s ", cleanPort)) || strings.HasSuffix(line, fmt.Sprintf("--dport %s", cleanPort))) &&
					(strings.Contains(line, "-j DNAT") || strings.Contains(line, "-j REDIRECT")) {
					delRule := strings.Replace(line, "-A ", "-D ", 1)
					args := strings.Fields(delRule)
					_ = exec.Command("iptables", append([]string{"-t", "nat"}, args...)...).Run()
				}
			}
		}

		// Remove any explicit DROP/REJECT rules in INPUT targeting this port
		if inOut, err := exec.Command("iptables", "-S", "INPUT").Output(); err == nil {
			for _, line := range strings.Split(string(inOut), "\n") {
				line = strings.TrimSpace(line)
				if (strings.Contains(line, fmt.Sprintf("--dport %s ", cleanPort)) || strings.HasSuffix(line, fmt.Sprintf("--dport %s", cleanPort))) &&
					(strings.Contains(line, "-j DROP") || strings.Contains(line, "-j REJECT")) {
					delRule := strings.Replace(line, "-A ", "-D ", 1)
					args := strings.Fields(delRule)
					_ = exec.Command("iptables", args...).Run()
				}
			}
		}

		// Direct cleanup loop
		_ = exec.Command("bash", "-c", fmt.Sprintf("while iptables -t nat -D PREROUTING -p tcp --dport %s -j DNAT 2>/dev/null; do :; done", cleanPort)).Run()
		_ = exec.Command("bash", "-c", fmt.Sprintf("while iptables -t nat -D PREROUTING -p udp --dport %s -j DNAT 2>/dev/null; do :; done", cleanPort)).Run()
		_ = exec.Command("bash", "-c", fmt.Sprintf("while iptables -t nat -D PREROUTING -p tcp --dport %s -j REDIRECT 2>/dev/null; do :; done", cleanPort)).Run()
		_ = exec.Command("bash", "-c", fmt.Sprintf("while iptables -t nat -D PREROUTING -p udp --dport %s -j REDIRECT 2>/dev/null; do :; done", cleanPort)).Run()
		_ = exec.Command("bash", "-c", fmt.Sprintf("while iptables -D INPUT -p tcp --dport %s -j DROP 2>/dev/null; do :; done", cleanPort)).Run()
		_ = exec.Command("bash", "-c", fmt.Sprintf("while iptables -D INPUT -p udp --dport %s -j DROP 2>/dev/null; do :; done", cleanPort)).Run()
	}

	// 5. Setup dedicated tunnel traffic accounting chains (accurate bytes, no noise)
	SetupAccountingChains(cfg)
}

func SetupAccountingChains(cfg config.TunnelConfig) {
	if os.Geteuid() != 0 {
		return
	}

	// 1. Create chains if not present
	_ = exec.Command("iptables", "-N", "NORA_ACCT_IN").Run()
	_ = exec.Command("iptables", "-N", "NORA_ACCT_OUT").Run()
	_ = exec.Command("iptables", "-F", "NORA_ACCT_IN").Run()
	_ = exec.Command("iptables", "-F", "NORA_ACCT_OUT").Run()

	// 2. Attach hooks to top of INPUT and OUTPUT
	_ = exec.Command("iptables", "-D", "INPUT", "-j", "NORA_ACCT_IN").Run()
	_ = exec.Command("iptables", "-I", "INPUT", "1", "-j", "NORA_ACCT_IN").Run()

	_ = exec.Command("iptables", "-D", "OUTPUT", "-j", "NORA_ACCT_OUT").Run()
	_ = exec.Command("iptables", "-I", "OUTPUT", "1", "-j", "NORA_ACCT_OUT").Run()

	// 3. Control Port accounting
	if cfg.ControlPort > 0 {
		cpStr := fmt.Sprintf("%d", cfg.ControlPort)
		_ = exec.Command("iptables", "-A", "NORA_ACCT_IN", "-p", "tcp", "--dport", cpStr).Run()
		_ = exec.Command("iptables", "-A", "NORA_ACCT_OUT", "-p", "tcp", "--sport", cpStr).Run()
		_ = exec.Command("iptables", "-A", "NORA_ACCT_IN", "-p", "tcp", "--sport", cpStr).Run()
		_ = exec.Command("iptables", "-A", "NORA_ACCT_OUT", "-p", "tcp", "--dport", cpStr).Run()
	}

	// 4. Data Ports accounting
	for _, p := range cfg.Ports {
		cleanPort := strings.TrimSpace(p)
		if strings.Contains(cleanPort, "=") {
			cleanPort = strings.Split(cleanPort, "=")[0]
		}
		cleanPort = strings.TrimSpace(cleanPort)
		if cleanPort == "" {
			continue
		}
		_ = exec.Command("iptables", "-A", "NORA_ACCT_IN", "-p", "tcp", "--dport", cleanPort).Run()
		_ = exec.Command("iptables", "-A", "NORA_ACCT_OUT", "-p", "tcp", "--sport", cleanPort).Run()
		_ = exec.Command("iptables", "-A", "NORA_ACCT_IN", "-p", "tcp", "--sport", cleanPort).Run()
		_ = exec.Command("iptables", "-A", "NORA_ACCT_OUT", "-p", "tcp", "--dport", cleanPort).Run()
	}
}
