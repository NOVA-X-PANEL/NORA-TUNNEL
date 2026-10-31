package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/NOVA-X-PANEL/NORA-TUNNEL/internal/backhaul"
	"github.com/NOVA-X-PANEL/NORA-TUNNEL/internal/config"
	"github.com/NOVA-X-PANEL/NORA-TUNNEL/internal/shadow"
	"github.com/NOVA-X-PANEL/NORA-TUNNEL/internal/web"
)

var (
	Version   = "1.2.2"
	BuildDate = ""
)

func main() {
	configPath := flag.String("c", "", "Path to config file (default: /etc/nora-tunnel/config.json or ./config.json)")
	portFlag := flag.Int("p", 0, "Web UI Port (override config)")
	showVer := flag.Bool("v", false, "Show version")
	setUser := flag.String("set-user", "", "Set admin username and exit")
	setPass := flag.String("set-pass", "", "Set admin password and exit")
	resetTunnel := flag.Bool("reset-tunnel", false, "Reset and remove all tunnel configurations")
	flag.Parse()

	cleanVer := "v" + strings.TrimPrefix(Version, "v")
	if *showVer {
		fmt.Printf("NORA TUNNEL %s (Built %s)\n", cleanVer, BuildDate)
		return
	}

	// Load configuration
	appCfg, err := config.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("[NORA-FATAL] Failed to load config: %v", err)
	}

	if *setUser != "" || *setPass != "" {
		if *setUser != "" {
			appCfg.Username = *setUser
		}
		if *setPass != "" {
			appCfg.Password = *setPass
		}
		if err := appCfg.Save(); err != nil {
			log.Fatalf("[NORA-FATAL] Failed to update credentials: %v", err)
		}
		fmt.Printf("Admin credentials updated successfully (user: %s).\n", appCfg.Username)
		return
	}

	if *resetTunnel {
		cfgMgr := config.NewManager(appCfg)
		bhMgr := backhaul.NewManager()
		_ = bhMgr.Reset()
		if err := cfgMgr.ResetTunnel(); err != nil {
			log.Fatalf("[NORA-FATAL] Failed to reset tunnel config: %v", err)
		}
		fmt.Println("[✔] Tunnel completely removed and reset.")
		return
	}

	log.Printf("[NORA] Starting NORA TUNNEL %s...", cleanVer)

	if *portFlag > 0 {
		appCfg.WebPort = *portFlag
	}

	cfgMgr := config.NewManager(appCfg)

	// Backhaul & Nora Shadow Managers
	bhMgr := backhaul.NewManager()
	shadowMgr := shadow.NewManager()

	// Ensure Backhaul core binary is available
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	binPath, err := bhMgr.EnsureBinary(ctx)
	if err != nil {
		log.Printf("[NORA-WARN] Backhaul binary check failed: %v", err)
	} else {
		log.Printf("[NORA] Backhaul core binary verified at: %s", binPath)
	}

	// Auto-start tunnel if configured
	tunCfg := cfgMgr.Get()
	if tunCfg.Role != "" && tunCfg.Role != "none" {
		tunCfg.Enabled = true
		_ = cfgMgr.Update(func(c *config.TunnelConfig) { c.Enabled = true })
		if tunCfg.Core == "shadow" {
			log.Printf("[NORA] Auto-starting existing Nora Shadow tunnel (%s / %s)...", tunCfg.Role, tunCfg.Mode)
			if err := shadowMgr.Start(tunCfg); err != nil {
				log.Printf("[NORA-ERROR] Failed to auto-start Nora Shadow tunnel: %v", err)
			}
		} else {
			st := bhMgr.GetStatus()
			if !st.Running {
				log.Printf("[NORA] Auto-starting existing Backhaul tunnel (%s / %s)...", tunCfg.Role, tunCfg.Mode)
				if err := bhMgr.Start(tunCfg); err != nil {
					log.Printf("[NORA-ERROR] Failed to auto-start Backhaul tunnel: %v", err)
				}
			} else {
				log.Printf("[NORA] Backhaul tunnel is already actively running (%s / %s).", tunCfg.Role, tunCfg.Mode)
			}
		}
	}

	// Web Server with resilient listener and auto port cleanup
	srv := web.NewServer(cfgMgr, bhMgr, shadowMgr, Version)
	addr := fmt.Sprintf("0.0.0.0:%d", appCfg.WebPort)

	httpServer := &http.Server{
		Addr:         addr,
		Handler:      srv,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	listener, err := createResilientListener(appCfg.WebPort)
	if err != nil {
		log.Fatalf("[NORA-FATAL] HTTP server failed to bind port %d: %v", appCfg.WebPort, err)
	}

	go func() {
		log.Printf("[NORA] Web Dashboard running on http://%s", addr)
		if err := httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[NORA-FATAL] HTTP server error: %v", err)
		}
	}()

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigChan

	log.Printf("[NORA] Received signal %v, shutting down web server...", sig)
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	_ = httpServer.Shutdown(shutdownCtx)
	log.Printf("[NORA] Web server stopped cleanly (tunnel services remain running in background).")
}

func createResilientListener(port int) (net.Listener, error) {
	addr := fmt.Sprintf("0.0.0.0:%d", port)
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
				// SO_REUSEPORT (15 on Linux)
				_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, 15, 1)
			})
		},
	}

	var listener net.Listener
	var err error

	for attempt := 1; attempt <= 5; attempt++ {
		listener, err = lc.Listen(context.Background(), "tcp", addr)
		if err == nil {
			return listener, nil
		}

		log.Printf("[NORA-WARN] Web port %d is occupied or releasing (attempt %d/5): %v", port, attempt, err)
		if os.Geteuid() == 0 {
			// Proactively terminate any zombie/orphaned process on this port
			_ = exec.Command("fuser", "-k", fmt.Sprintf("%d/tcp", port)).Run()
		}
		time.Sleep(800 * time.Millisecond)
	}

	return nil, fmt.Errorf("failed to bind web port %d after 5 retries: %w", port, err)
}
