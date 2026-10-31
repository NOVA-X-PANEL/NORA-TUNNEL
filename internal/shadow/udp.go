package shadow

import (
	"encoding/binary"
	"io"
	"net"
	"sync"
	"time"
)

// Frame format:
// [2 bytes uint16 payload length] [2 bytes uint16 port] [8 bytes uint64 sessionID] [payload data...]
const (
	headerSize = 12 // 2 + 2 + 8
)

type udpSessionTracker struct {
	mu       sync.RWMutex
	byAddr   map[string]uint64
	byID     map[uint64]*net.UDPAddr
	lastSeen map[uint64]time.Time
	nextID   uint64
}

func newUDPSessionTracker() *udpSessionTracker {
	t := &udpSessionTracker{
		byAddr:   make(map[string]uint64),
		byID:     make(map[uint64]*net.UDPAddr),
		lastSeen: make(map[uint64]time.Time),
		nextID:   1,
	}
	go t.cleanupLoop()
	return t
}

func (t *udpSessionTracker) getOrCreate(addr *net.UDPAddr) uint64 {
	key := addr.String()
	t.mu.Lock()
	defer t.mu.Unlock()

	if id, ok := t.byAddr[key]; ok {
		t.lastSeen[id] = time.Now()
		return id
	}

	id := t.nextID
	t.nextID++
	t.byAddr[key] = id
	t.byID[id] = addr
	t.lastSeen[id] = time.Now()
	return id
}

func (t *udpSessionTracker) getAddr(id uint64) (*net.UDPAddr, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	addr, ok := t.byID[id]
	return addr, ok
}

func (t *udpSessionTracker) cleanupLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		t.mu.Lock()
		for id, seen := range t.lastSeen {
			if now.Sub(seen) > 2*time.Minute {
				if addr, ok := t.byID[id]; ok {
					delete(t.byAddr, addr.String())
				}
				delete(t.byID, id)
				delete(t.lastSeen, id)
			}
		}
		t.mu.Unlock()
	}
}

func writeUDPFrame(w io.Writer, port uint16, sessionID uint64, data []byte) error {
	totalLen := uint16(len(data))
	hdr := make([]byte, headerSize)
	binary.BigEndian.PutUint16(hdr[0:2], totalLen)
	binary.BigEndian.PutUint16(hdr[2:4], port)
	binary.BigEndian.PutUint64(hdr[4:12], sessionID)

	if _, err := w.Write(hdr); err != nil {
		return err
	}
	_, err := w.Write(data)
	return err
}

func readUDPFrame(r io.Reader) (port uint16, sessionID uint64, data []byte, err error) {
	hdr := make([]byte, headerSize)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return 0, 0, nil, err
	}

	totalLen := binary.BigEndian.Uint16(hdr[0:2])
	port = binary.BigEndian.Uint16(hdr[2:4])
	sessionID = binary.BigEndian.Uint64(hdr[4:12])

	data = make([]byte, totalLen)
	if _, err := io.ReadFull(r, data); err != nil {
		return 0, 0, nil, err
	}
	return port, sessionID, data, nil
}
