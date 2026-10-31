package watchdog

import (
	"net"
	"testing"

	"github.com/NOVA-X-PANEL/NORA-TUNNEL/internal/backhaul"
	"github.com/NOVA-X-PANEL/NORA-TUNNEL/internal/config"
	"github.com/NOVA-X-PANEL/NORA-TUNNEL/internal/shadow"
)

func TestWatchdogProbeSuccess(t *testing.T) {
	// Start a local dummy TCP listener to simulate peer
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to bind test listener: %v", err)
	}
	defer l.Close()

	port := l.Addr().(*net.TCPAddr).Port

	appCfg := config.DefaultConfig()
	appCfg.Tunnel = config.TunnelConfig{
		Enabled:     true,
		Role:        "kharej",
		Mode:        "reverse",
		IranIP:      "127.0.0.1",
		ControlPort: port,
	}

	cfgMgr := config.NewManager(appCfg)
	bhMgr := backhaul.NewManager()
	shadowMgr := shadow.NewManager()

	wd := NewWatchdog(cfgMgr, bhMgr, shadowMgr)
	defer wd.Stop()

	success, lat := wd.probePeer(appCfg.Tunnel)
	if !success {
		t.Errorf("Expected probe to succeed against listening port %d, got failed", port)
	}
	if lat < 0 {
		t.Errorf("Expected latency >= 0, got %d", lat)
	}
}

func TestWatchdogProbeFailure(t *testing.T) {
	// Pick an unlikely bound local port
	appCfg := config.DefaultConfig()
	appCfg.Tunnel = config.TunnelConfig{
		Enabled:     true,
		Role:        "kharej",
		Mode:        "reverse",
		IranIP:      "127.0.0.1",
		ControlPort: 54321, // nothing listening
	}

	cfgMgr := config.NewManager(appCfg)
	bhMgr := backhaul.NewManager()
	shadowMgr := shadow.NewManager()

	wd := NewWatchdog(cfgMgr, bhMgr, shadowMgr)
	defer wd.Stop()

	success, lat := wd.probePeer(appCfg.Tunnel)
	if success {
		t.Errorf("Expected probe to fail against port 54321, got success")
	}
	if lat != -1 {
		t.Errorf("Expected latency -1, got %d", lat)
	}
}

func TestWatchdogStatus(t *testing.T) {
	appCfg := config.DefaultConfig()
	appCfg.Tunnel = config.TunnelConfig{
		Enabled:     true,
		Role:        "kharej",
		Mode:        "reverse",
		IranIP:      "127.0.0.1",
		ControlPort: 3080,
	}

	cfgMgr := config.NewManager(appCfg)
	bhMgr := backhaul.NewManager()
	shadowMgr := shadow.NewManager()

	wd := NewWatchdog(cfgMgr, bhMgr, shadowMgr)
	defer wd.Stop()

	st := wd.GetStatus()
	if !st.Enabled {
		t.Errorf("Expected watchdog enabled by default")
	}
	if !st.Active {
		t.Errorf("Expected watchdog active when tunnel is configured")
	}
	if st.FailureThreshold != 4 {
		t.Errorf("Expected failure threshold 4, got %d", st.FailureThreshold)
	}

	wd.SetEnabled(false)
	st2 := wd.GetStatus()
	if st2.Enabled {
		t.Errorf("Expected watchdog disabled after SetEnabled(false)")
	}
}
