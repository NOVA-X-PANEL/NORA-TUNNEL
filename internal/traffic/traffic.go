package traffic

import (
	"bufio"
	"fmt"
	"os"
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

func (c *Collector) collectOnce() {
	file, err := os.Open("/proc/net/dev")
	if err != nil {
		return
	}
	defer file.Close()

	var totalRx, totalTx uint64
	scanner := bufio.NewScanner(file)
	lineIdx := 0

	for scanner.Scan() {
		lineIdx++
		if lineIdx <= 2 {
			continue // Header lines
		}

		line := scanner.Text()
		parts := strings.Split(line, ":")
		if len(parts) != 2 {
			continue
		}

		iface := strings.TrimSpace(parts[0])
		// Ignore loopback and virtual/docker interfaces
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

	now := time.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

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
