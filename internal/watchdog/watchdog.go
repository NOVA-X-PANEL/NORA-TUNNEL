package watchdog

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/NOVA-X-PANEL/NORA-TUNNEL/internal/backhaul"
	"github.com/NOVA-X-PANEL/NORA-TUNNEL/internal/config"
	"github.com/NOVA-X-PANEL/NORA-TUNNEL/internal/shadow"
)

type Status struct {
	Enabled             bool      `json:"enabled"`
	Active              bool      `json:"active"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
	FailureThreshold    int       `json:"failure_threshold"`
	CheckIntervalSec    int       `json:"check_interval_sec"`
	LastCheck           time.Time `json:"last_check"`
	LastSuccess         time.Time `json:"last_success"`
	LastAutoRestart     time.Time `json:"last_auto_restart"`
	AutoRestartCount    int64     `json:"auto_restart_count"`
	LastPingMs          int64     `json:"last_ping_ms"`
	LastMessage         string    `json:"last_message"`
}

type Watchdog struct {
	cfgMgr    *config.Manager
	bhMgr     *backhaul.Manager
	shadowMgr *shadow.Manager

	mu                  sync.RWMutex
	enabled             bool
	failureThreshold    int
	checkInterval       time.Duration
	cooldown            time.Duration
	consecutiveFailures int
	autoRestartCount    int64
	lastCheck           time.Time
	lastSuccess         time.Time
	lastAutoRestart     time.Time
	lastPingMs          int64
	lastMessage         string

	ctx    context.Context
	cancel context.CancelFunc
}

func NewWatchdog(cfgMgr *config.Manager, bhMgr *backhaul.Manager, shadowMgr *shadow.Manager) *Watchdog {
	ctx, cancel := context.WithCancel(context.Background())
	return &Watchdog{
		cfgMgr:           cfgMgr,
		bhMgr:            bhMgr,
		shadowMgr:        shadowMgr,
		enabled:          true,
		failureThreshold: 4, // 4 failures * 6s = 24 seconds of confirmed persistent outage
		checkInterval:    6 * time.Second,
		cooldown:         25 * time.Second,
		lastPingMs:       -1,
		lastMessage:      "سامانه محافظ خودکار نورا فعال است",
		ctx:              ctx,
		cancel:           cancel,
	}
}

func (w *Watchdog) Start() {
	go w.loop()
	log.Printf("[NORA-WATCHDOG] Self-healing Watchdog started (interval: %v, threshold: %d probes)", w.checkInterval, w.failureThreshold)
}

func (w *Watchdog) Stop() {
	if w.cancel != nil {
		w.cancel()
	}
}

func (w *Watchdog) SetEnabled(enabled bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.enabled = enabled
	if enabled {
		w.lastMessage = "محافظ خودکار فعال شد"
	} else {
		w.lastMessage = "محافظ خودکار موقتاً غیرفعال شد"
	}
}

func (w *Watchdog) ResetFailures() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.consecutiveFailures = 0
}

func (w *Watchdog) TriggerManualRestart() error {
	w.mu.Lock()
	cfg := w.cfgMgr.Get()
	w.consecutiveFailures = 0
	w.lastAutoRestart = time.Now()
	w.mu.Unlock()

	if cfg.Core == "shadow" {
		return w.shadowMgr.Restart(cfg)
	}
	return w.bhMgr.Restart(cfg)
}

func (w *Watchdog) GetStatus() Status {
	w.mu.RLock()
	defer w.mu.RUnlock()

	cfg := w.cfgMgr.Get()
	active := cfg.Enabled && cfg.Role != "" && cfg.Role != "none"

	return Status{
		Enabled:             w.enabled,
		Active:              active,
		ConsecutiveFailures: w.consecutiveFailures,
		FailureThreshold:    w.failureThreshold,
		CheckIntervalSec:    int(w.checkInterval.Seconds()),
		LastCheck:           w.lastCheck,
		LastSuccess:         w.lastSuccess,
		LastAutoRestart:     w.lastAutoRestart,
		AutoRestartCount:    w.autoRestartCount,
		LastPingMs:          w.lastPingMs,
		LastMessage:         w.lastMessage,
	}
}

func (w *Watchdog) loop() {
	ticker := time.NewTicker(w.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
			w.checkOnce()
		}
	}
}

func (w *Watchdog) checkOnce() {
	w.mu.RLock()
	isEnabled := w.enabled
	w.mu.RUnlock()

	if !isEnabled {
		return
	}

	cfg := w.cfgMgr.Get()
	if !cfg.Enabled || cfg.Role == "" || cfg.Role == "none" {
		w.mu.Lock()
		w.consecutiveFailures = 0
		w.lastPingMs = -1
		w.mu.Unlock()
		return
	}

	success, latencyMs := w.probePeer(cfg)

	w.mu.Lock()
	defer w.mu.Unlock()

	w.lastCheck = time.Now()

	if success {
		if w.consecutiveFailures > 0 {
			log.Printf("[NORA-WATCHDOG] ✅ Tunnel connection recovered healthy (ping: %d ms)", latencyMs)
		}
		w.consecutiveFailures = 0
		w.lastPingMs = latencyMs
		w.lastSuccess = time.Now()
		w.lastMessage = fmt.Sprintf("ارتباط تانل فعال و پایدار است (پینگ: %d ms)", latencyMs)
		w.bhMgr.UpdatePing(latencyMs)
		return
	}

	// Probe failed
	w.consecutiveFailures++
	w.lastPingMs = -1
	w.bhMgr.UpdatePing(-1)
	w.lastMessage = fmt.Sprintf("عدم پاسخگویی سرور مقابل (خطای متوالی: %d/%d)", w.consecutiveFailures, w.failureThreshold)

	// Evaluate auto-restart threshold
	if w.consecutiveFailures >= w.failureThreshold {
		now := time.Now()
		if now.Sub(w.lastAutoRestart) >= w.cooldown {
			w.lastAutoRestart = now
			w.autoRestartCount++
			w.consecutiveFailures = 0
			restartNum := w.autoRestartCount

			log.Printf("[NORA-WATCHDOG] ⚠️ Tunnel drop detected (%d consecutive failures). Initiating automated self-healing restart (#%d)...", w.failureThreshold, restartNum)
			w.lastMessage = fmt.Sprintf("بازیابی خودکار #%d در حال انجام است...", restartNum)

			// Async restart to not block the watchdog ticker
			go func(c config.TunnelConfig, num int64) {
				var err error
				if c.Core == "shadow" {
					err = w.shadowMgr.Restart(c)
				} else {
					err = w.bhMgr.Restart(c)
				}

				w.mu.Lock()
				defer w.mu.Unlock()
				if err != nil {
					log.Printf("[NORA-WATCHDOG] ❌ Auto-restart #%d failed: %v", num, err)
					w.lastMessage = fmt.Sprintf("خطا در بازیابی خودکار #%d: %v", num, err)
				} else {
					log.Printf("[NORA-WATCHDOG] 🚀 Auto-restart #%d completed successfully.", num)
					w.lastMessage = fmt.Sprintf("بازیابی خودکار #%d با موفقیت تکمیل شد (%s)", num, time.Now().Format("15:04:05"))
				}
			}(cfg, restartNum)
		} else {
			log.Printf("[NORA-WATCHDOG] Cooldown active (last restart %v ago). Awaiting next probe window.", time.Since(w.lastAutoRestart).Round(time.Second))
		}
	}
}

func (w *Watchdog) probePeer(cfg config.TunnelConfig) (bool, int64) {
	ctrlPort := cfg.ControlPort
	if ctrlPort <= 0 {
		ctrlPort = 3080
	}

	targets := make([]string, 0, 4)

	if cfg.Role == "kharej" {
		targetIP := cfg.IranIP
		if targetIP == "" {
			return false, -1
		}
		// In reverse mode, Iran is server listening on ControlPort
		targets = append(targets, fmt.Sprintf("%s:%d", targetIP, ctrlPort))
		targets = append(targets, fmt.Sprintf("%s:7777", targetIP))
		if len(cfg.Ports) > 0 {
			cleanP := strings.TrimSpace(cfg.Ports[0])
			if strings.Contains(cleanP, "=") {
				cleanP = strings.Split(cleanP, "=")[0]
			}
			cleanP = strings.TrimSpace(cleanP)
			if cleanP != "" {
				targets = append(targets, fmt.Sprintf("%s:%s", targetIP, cleanP))
			}
		}
	} else if cfg.Role == "iran" {
		targetIP := cfg.KharejIP
		if cfg.Mode == "reverse" {
			// On Iran in reverse mode: ensure local server is bound
			targets = append(targets, fmt.Sprintf("127.0.0.1:%d", ctrlPort))
			if targetIP != "" {
				targets = append(targets, fmt.Sprintf("%s:7777", targetIP))
			}
		} else {
			// Direct mode: Kharej is server
			if targetIP == "" {
				return false, -1
			}
			targets = append(targets, fmt.Sprintf("%s:%d", targetIP, ctrlPort))
			targets = append(targets, fmt.Sprintf("%s:7777", targetIP))
		}
	}

	for _, addr := range targets {
		start := time.Now()
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err == nil {
			_ = conn.Close()
			latencyMs := time.Since(start).Milliseconds()
			return true, latencyMs
		}
	}

	return false, -1
}
