package shadow

import (
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"
)

type Client struct {
	remoteHost string
	remotePort int
	token      string
	ports      []string
	ctx        context.Context
	cancel     context.CancelFunc

	mu           sync.Mutex
	sshClient    *ssh.Client
	tcpListeners map[string]net.Listener
	udpListeners map[string]*net.UDPConn
	udpRemotePorts map[uint16]*net.UDPConn
	udpTracker   *udpSessionTracker
	udpChan      ssh.Channel
	udpChanMu    sync.Mutex

	lastPingMs int64
	connected  int32 // 1 = connected, 0 = disconnected
	lastError  string
}

func StartClient(ctx context.Context, remoteHost string, remotePort int, token string, ports []string) *Client {
	cCtx, cCancel := context.WithCancel(ctx)
	c := &Client{
		remoteHost:   remoteHost,
		remotePort:   remotePort,
		token:        token,
		ports:        ports,
		ctx:          cCtx,
		cancel:       cCancel,
		tcpListeners: make(map[string]net.Listener),
		udpListeners: make(map[string]*net.UDPConn),
		udpRemotePorts: make(map[uint16]*net.UDPConn),
		udpTracker:   newUDPSessionTracker(),
	}

	go c.lifecycleLoop()
	return c
}

func (c *Client) Close() {
	c.cancel()
	c.mu.Lock()
	defer c.mu.Unlock()

	c.closeListenersLocked()
	if c.sshClient != nil {
		_ = c.sshClient.Close()
		c.sshClient = nil
	}
	atomic.StoreInt32(&c.connected, 0)
}

func (c *Client) IsConnected() bool {
	return atomic.LoadInt32(&c.connected) == 1
}

func (c *Client) LastPing() int {
	return int(atomic.LoadInt64(&c.lastPingMs))
}

func (c *Client) LastErr() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastError
}

func (c *Client) lifecycleLoop() {
	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		err := c.runOnce()
		if err != nil {
			c.mu.Lock()
			c.lastError = err.Error()
			c.mu.Unlock()
		}

		select {
		case <-c.ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}

func (c *Client) runOnce() error {
	addr := net.JoinHostPort(c.remoteHost, strconv.Itoa(c.remotePort))
	cfg := &ssh.ClientConfig{
		User: "nora-shadow",
		Auth: []ssh.AuthMethod{
			ssh.Password(c.token),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
		ClientVersion:   "SSH-2.0-OpenSSH_9.6p1 Ubuntu-3ubuntu13.8",
	}

	rawConn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		atomic.StoreInt32(&c.connected, 0)
		return fmt.Errorf("shadow connection failed to %s: %w", addr, err)
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(rawConn, addr, cfg)
	if err != nil {
		_ = rawConn.Close()
		atomic.StoreInt32(&c.connected, 0)
		return fmt.Errorf("ssh handshake failed: %w", err)
	}

	client := ssh.NewClient(sshConn, chans, reqs)
	c.mu.Lock()
	c.sshClient = client
	c.lastError = ""
	c.mu.Unlock()
	atomic.StoreInt32(&c.connected, 1)

	// Open nora-udp channel for UDP multiplexing
	udpChan, reqsUDP, err := client.OpenChannel("nora-udp", nil)
	if err == nil {
		go ssh.DiscardRequests(reqsUDP)
		c.udpChanMu.Lock()
		c.udpChan = udpChan
		c.udpChanMu.Unlock()
		go c.handleUDPIncomingFrames(udpChan)
	}

	// Start port forwarders on Iran
	if err := c.setupForwarders(); err != nil {
		c.Close()
		return err
	}

	// Keepalive and health monitor loop
	pingTicker := time.NewTicker(4 * time.Second)
	defer pingTicker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return nil
		case <-pingTicker.C:
			t0 := time.Now()
			_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
			if err != nil {
				atomic.StoreInt32(&c.connected, 0)
				c.mu.Lock()
				c.closeListenersLocked()
				c.mu.Unlock()
				_ = client.Close()
				return fmt.Errorf("keepalive failed: %w", err)
			}
			rtt := time.Since(t0).Milliseconds()
			atomic.StoreInt64(&c.lastPingMs, rtt)
		}
	}
}

func (c *Client) setupForwarders() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.closeListenersLocked()

	for _, pStr := range c.ports {
		pStr = strings.TrimSpace(pStr)
		if pStr == "" {
			continue
		}
			localPort, remotePort, err := parsePortMapping(pStr)
			if err != nil {
				continue
			}

			// 1. TCP Listener on 0.0.0.0:port
			tcpL, err := net.Listen("tcp", fmt.Sprintf(":%d", localPort))
			if err == nil {
				c.tcpListeners[strconv.Itoa(localPort)] = tcpL
				go c.forwardTCPPort(tcpL, uint16(remotePort))
			}

			// 2. UDP Listener on 0.0.0.0:port
			uAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", localPort))
			if err == nil {
				uConn, err := net.ListenUDP("udp", uAddr)
				if err == nil {
					c.udpListeners[strconv.Itoa(localPort)] = uConn
					c.udpRemotePorts[uint16(remotePort)] = uConn
					go c.forwardUDPPort(uConn, uint16(remotePort))
				}
		}
	}
	return nil
}

func parsePortMapping(raw string) (localPort, remotePort int, err error) {
	parts := strings.FieldsFunc(strings.TrimSpace(raw), func(r rune) bool { return r == '=' || r == ':' })
	if len(parts) == 0 || len(parts) > 2 {
		return 0, 0, fmt.Errorf("invalid port mapping %q", raw)
	}
	localPort, err = strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || localPort <= 0 || localPort > 65535 {
		return 0, 0, fmt.Errorf("invalid local port %q", raw)
	}
	remotePort = localPort
	if len(parts) == 2 {
		remotePort, err = strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil || remotePort <= 0 || remotePort > 65535 {
			return 0, 0, fmt.Errorf("invalid remote port %q", raw)
		}
	}
	return localPort, remotePort, nil
}

func (c *Client) forwardTCPPort(l net.Listener, port uint16) {
	for {
		conn, err := l.Accept()
		if err != nil {
			return
		}

		go func(localConn net.Conn) {
			defer localConn.Close()
			c.mu.Lock()
			client := c.sshClient
			c.mu.Unlock()

			if client == nil {
				return
			}

			remoteConn, err := client.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
			if err != nil {
				return
			}
			defer remoteConn.Close()

			done := make(chan struct{}, 2)
			go func() {
				_, _ = io.Copy(remoteConn, localConn)
				done <- struct{}{}
			}()
			go func() {
				_, _ = io.Copy(localConn, remoteConn)
				done <- struct{}{}
			}()
			<-done
		}(conn)
	}
}

func (c *Client) forwardUDPPort(uConn *net.UDPConn, port uint16) {
	buf := make([]byte, 65535)
	for {
		n, clientAddr, err := uConn.ReadFromUDP(buf)
		if err != nil {
			return
		}

		c.udpChanMu.Lock()
		ch := c.udpChan
		c.udpChanMu.Unlock()

		if ch == nil {
			continue
		}

		sessionID := c.udpTracker.getOrCreate(clientAddr)
		c.udpChanMu.Lock()
		_ = writeUDPFrame(ch, port, sessionID, buf[:n])
		c.udpChanMu.Unlock()
	}
}

func (c *Client) handleUDPIncomingFrames(ch ssh.Channel) {
	for {
		port, sessionID, data, err := readUDPFrame(ch)
		if err != nil {
			return
		}

		clientAddr, ok := c.udpTracker.getAddr(sessionID)
		if !ok {
			continue
		}

			c.mu.Lock()
			uConn, exists := c.udpRemotePorts[port]
			c.mu.Unlock()

		if exists && uConn != nil {
			_, _ = uConn.WriteToUDP(data, clientAddr)
		}
	}
}

func (c *Client) closeListenersLocked() {
	for _, l := range c.tcpListeners {
		_ = l.Close()
	}
	c.tcpListeners = make(map[string]net.Listener)

	for _, u := range c.udpListeners {
		_ = u.Close()
	}
	c.udpListeners = make(map[string]*net.UDPConn)
	c.udpRemotePorts = make(map[uint16]*net.UDPConn)
}
