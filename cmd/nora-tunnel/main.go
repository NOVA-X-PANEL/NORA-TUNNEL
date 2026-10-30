package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/NOVA-X-PANEL/NORA-TUNNEL/internal/backhaul"
	"github.com/NOVA-X-PANEL/NORA-TUNNEL/internal/config"
	"github.com/NOVA-X-PANEL/NORA-TUNNEL/internal/web"
)

var (
	Version   = "1.1.5"
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

	// Backhaul Manager
	bhMgr := backhaul.NewManager()

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
		st := bhMgr.GetStatus()
		if !st.Running {
			log.Printf("[NORA] Auto-starting existing tunnel (%s / %s)...", tunCfg.Role, tunCfg.Mode)
			if err := bhMgr.Start(tunCfg); err != nil {
				log.Printf("[NORA-ERROR] Failed to auto-start tunnel: %v", err)
			}
		} else {
			log.Printf("[NORA] Tunnel is already actively running (%s / %s).", tunCfg.Role, tunCfg.Mode)
		}
	}

	// Web Server
	srv := web.NewServer(cfgMgr, bhMgr, Version)
	addr := fmt.Sprintf("0.0.0.0:%d", appCfg.WebPort)

	httpServer := &http.Server{
		Addr:         addr,
		Handler:      srv,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Printf("[NORA] Web Dashboard running on http://%s", addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
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
