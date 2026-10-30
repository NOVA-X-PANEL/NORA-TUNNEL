package traffic

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Stats struct {
	RxBytes        uint64 `json:"rx_bytes"`
	TxBytes        uint64 `json:"tx_bytes"`
	TotalBytes     uint64 `json:"total_bytes"`
	RxFormatted    string `json:"rx_formatted"`
	TxFormatted    string `json:"tx_formatted"`
	TotalFormatted string `json:"total_formatted"`
	RxSpeedBps     uint64 `json:"rx_speed_bps"`
	TxSpeedBps     uint64 `json:"tx_speed_bps"`
	RxSpeedStr     string `json:"rx_speed_str"`
	TxSpeedStr     string `json:"tx_speed_str"`
}

type Collector struct {
	mu       sync.RWMutex
	lastRx   uint64
	lastTx   uint64
	lastTime time.Time
	baseRx   uint64
	baseTx   uint64
	baseSet  bool
	current  Stats
}

var (
	defaultCollector *Collector
	once             sync.Once
)

func GetCollector() *Collector {
	once.Do(func() {
		defaultCollector = &Collector{
			lastTime: time.Now(),
		}
		defaultCollector.collectOnce()
		go defaultCollector.loop()
	})
	return defaultCollector
}

func (c *Collector) loop() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		c.collectOnce()
	}
}

func (c *Collector) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.baseSet = false
	c.lastRx = 0
	c.lastTx = 0
	c.lastTime = time.Now()

	// Zero iptables accounting chains if present
	_ = exec.Command("iptables", "-Z", "NORA_ACCT_IN").Run()
	_ = exec.Command("iptables", "-Z", "NORA_ACCT_OUT").Run()

	c.current = Stats{
		RxBytes:        0,
		TxBytes:        0,
		TotalBytes:     0,
		RxFormatted:    "0 B",
		TxFormatted:    "0 B",
		TotalFormatted: "0 B",
		RxSpeedBps:     0,
		TxSpeedBps:     0,
		RxSpeedStr:     "0 B/s",
		TxSpeedStr:     "0 B/s",
	}
}

func readIptablesChainBytes(chain string) (uint64, bool) {
	out, err := exec.Command("iptables", "-L", chain, "-v", "-n", "-x").Output()
	if err != nil {
		return 0, false
	}
	var total uint64
	lines := strings.Split(string(out), "\n")
	found := false
	for i, line := range lines {
		if i < 2 {
			continue // skip header
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			if b, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
				total += b
				found = true
			}
		}
	}
	return total, found
}

func (c *Collector) collectOnce() {
	var totalRx, totalTx uint64
	usedIptables := false

	// 1. Try reading dedicated tunnel accounting chains first
	rxAcct, okIn := readIptablesChainBytes("NORA_ACCT_IN")
	txAcct, okOut := readIptablesChainBytes("NORA_ACCT_OUT")
	if okIn && okOut {
		totalRx = rxAcct
		totalTx = txAcct
		usedIptables = true
	} else {
		// 2. Fallback to /proc/net/dev with baseline subtraction
		file, err := os.Open("/proc/net/dev")
		if err == nil {
			defer file.Close()
			scanner := bufio.NewScanner(file)
			lineIdx := 0
			for scanner.Scan() {
				lineIdx++
				if lineIdx <= 2 {
					continue
				}
				line := scanner.Text()
				parts := strings.Split(line, ":")
				if len(parts) != 2 {
					continue
				}
				iface := strings.TrimSpace(parts[0])
				if iface == "lo" || strings.HasPrefix(iface, "docker") || strings.HasPrefix(iface, "veth") ||
					strings.HasPrefix(iface, "br-") || strings.HasPrefix(iface, "dummy") {
					continue
				}
				fields := strings.Fields(parts[1])
				if len(fields) >= 9 {
					rx, _ := strconv.ParseUint(fields[0], 10, 64)
					tx, _ := strconv.ParseUint(fields[8], 10, 64)
					totalRx += rx
					totalTx += tx
				}
			}
		}
	}

	now := time.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	// If using /proc/net/dev, apply baseline subtraction so we start at 0
	if !usedIptables {
		if !c.baseSet && (totalRx > 0 || totalTx > 0) {
			c.baseRx = totalRx
			c.baseTx = totalTx
			c.baseSet = true
		}
		if totalRx >= c.baseRx {
			totalRx = totalRx - c.baseRx
		} else {
			totalRx = 0
		}
		if totalTx >= c.baseTx {
			totalTx = totalTx - c.baseTx
		} else {
			totalTx = 0
		}
	}

	var rxSpeed, txSpeed uint64
	if !c.lastTime.IsZero() && now.After(c.lastTime) {
		secs := now.Sub(c.lastTime).Seconds()
		if secs > 0 && c.lastRx > 0 && totalRx >= c.lastRx {
			rxSpeed = uint64(float64(totalRx-c.lastRx) / secs)
		}
		if secs > 0 && c.lastTx > 0 && totalTx >= c.lastTx {
			txSpeed = uint64(float64(totalTx-c.lastTx) / secs)
		}
	}

	c.lastRx = totalRx
	c.lastTx = totalTx
	c.lastTime = now

	c.current = Stats{
		RxBytes:        totalRx,
		TxBytes:        totalTx,
		TotalBytes:     totalRx + totalTx,
		RxFormatted:    FormatBytes(totalRx),
		TxFormatted:    FormatBytes(totalTx),
		TotalFormatted: FormatBytes(totalRx + totalTx),
		RxSpeedBps:     rxSpeed,
		TxSpeedBps:     txSpeed,
		RxSpeedStr:     FormatSpeed(rxSpeed),
		TxSpeedStr:     FormatSpeed(txSpeed),
	}
}

func (c *Collector) GetStats() Stats {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.current
}

func FormatBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func FormatSpeed(bps uint64) string {
	return FormatBytes(bps) + "/s"
}
