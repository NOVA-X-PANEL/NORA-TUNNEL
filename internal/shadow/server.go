package shadow

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

type directTCPIPPayload struct {
	DestAddr string
	DestPort uint32
	OrigAddr string
	OrigPort uint32
}

func getOrCreateHostKey(keyPath string) (ssh.Signer, error) {
	if keyPath == "" {
		keyPath = "/etc/nora-tunnel/shadow_host_key"
		if os.Geteuid() != 0 {
			keyPath = "./shadow_host_key"
		}
	}

	_ = os.MkdirAll(filepath.Dir(keyPath), 0755)

	if data, err := os.ReadFile(keyPath); err == nil && len(data) > 0 {
		signer, err := ssh.ParsePrivateKey(data)
		if err == nil {
			return signer, nil
		}
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate ed25519 key: %w", err)
	}

	privBytes, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal private key: %w", err)
	}

	sshPub, err := ssh.NewPublicKey(priv.Public())
	if err != nil {
		return nil, fmt.Errorf("failed to create ssh public key: %w", err)
	}

	_ = os.WriteFile(keyPath, ssh.MarshalAuthorizedKey(sshPub), 0644)
	_ = os.WriteFile(keyPath+".priv", privBytes.Bytes, 0600)

	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return nil, fmt.Errorf("failed to create signer: %w", err)
	}
	return signer, nil
}

type Server struct {
	listener net.Listener
	token    string
	closing  chan struct{}
	mu       sync.Mutex
}

func StartServer(listenAddr string, token string, keyPath string) (*Server, error) {
	signer, err := getOrCreateHostKey(keyPath)
	if err != nil {
		return nil, err
	}

	config := &ssh.ServerConfig{
		ServerVersion: "SSH-2.0-OpenSSH_9.6p1 Ubuntu-3ubuntu13.8",
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if token == "" || string(pass) == token {
				return nil, nil
			}
			return nil, fmt.Errorf("invalid password/token for shadow user %s", c.User())
		},
	}
	config.AddHostKey(signer)

	l, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("shadow server listen error on %s: %w", listenAddr, err)
	}

	s := &Server{
		listener: l,
		token:    token,
		closing:  make(chan struct{}),
	}

	go s.acceptLoop(config)
	return s, nil
}

func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	select {
	case <-s.closing:
		return nil
	default:
		close(s.closing)
	}
	return s.listener.Close()
}

func (s *Server) acceptLoop(config *ssh.ServerConfig) {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.closing:
				return
			default:
				time.Sleep(100 * time.Millisecond)
				continue
			}
		}

		go s.handleConn(conn, config)
	}
}

func (s *Server) handleConn(conn net.Conn, config *ssh.ServerConfig) {
	sshConn, chans, reqs, err := ssh.NewServerConn(conn, config)
	if err != nil {
		_ = conn.Close()
		return
	}
	defer sshConn.Close()

	// Discard global requests except keepalive
	go func() {
		for req := range reqs {
			if req.Type == "keepalive@openssh.com" {
				_ = req.Reply(true, nil)
			} else {
				_ = req.Reply(false, nil)
			}
		}
	}()

	for newChan := range chans {
		switch newChan.ChannelType() {
		case "direct-tcpip":
			go s.handleDirectTCPIP(newChan)
		case "nora-udp":
			go s.handleNoraUDP(newChan)
		default:
			_ = newChan.Reject(ssh.UnknownChannelType, "unsupported channel type")
		}
	}
}

func (s *Server) handleDirectTCPIP(newChan ssh.NewChannel) {
	var payload directTCPIPPayload
	if err := ssh.Unmarshal(newChan.ExtraData(), &payload); err != nil {
		_ = newChan.Reject(ssh.ConnectionFailed, "bad payload")
		return
	}

	target := fmt.Sprintf("127.0.0.1:%d", payload.DestPort)
	destConn, err := net.DialTimeout("tcp", target, 5*time.Second)
	if err != nil {
		_ = newChan.Reject(ssh.ConnectionFailed, fmt.Sprintf("failed to dial target %s: %v", target, err))
		return
	}

	channel, requests, err := newChan.Accept()
	if err != nil {
		_ = destConn.Close()
		return
	}
	go ssh.DiscardRequests(requests)

	// Bi-directional copy
	go func() {
		defer destConn.Close()
		defer channel.Close()
		_, _ = io.Copy(destConn, channel)
	}()

	go func() {
		defer destConn.Close()
		defer channel.Close()
		_, _ = io.Copy(channel, destConn)
	}()
}

func (s *Server) handleNoraUDP(newChan ssh.NewChannel) {
	channel, requests, err := newChan.Accept()
	if err != nil {
		return
	}
	go ssh.DiscardRequests(requests)
	defer channel.Close()

	// Manage local UDP sockets for target services (e.g. 127.0.0.1:2087)
	type udpTargetSession struct {
		conn     *net.UDPConn
		lastSeen time.Time
	}

	var mu sync.Mutex
	targets := make(map[string]*udpTargetSession) // key: fmt.Sprintf("%d_%d", port, sessionID)

	// Cleanup old idle UDP connections
	done := make(chan struct{})
	defer close(done)

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				now := time.Now()
				mu.Lock()
				for k, sess := range targets {
					if now.Sub(sess.lastSeen) > 90*time.Second {
						_ = sess.conn.Close()
						delete(targets, k)
					}
				}
				mu.Unlock()
			}
		}
	}()

	// Reader from client
	for {
		port, sessionID, data, err := readUDPFrame(channel)
		if err != nil {
			return
		}

		key := fmt.Sprintf("%d_%d", port, sessionID)
		mu.Lock()
		sess, ok := targets[key]
		if !ok {
			raddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("127.0.0.1:%d", port))
			if err != nil {
				mu.Unlock()
				continue
			}

			uConn, err := net.DialUDP("udp", nil, raddr)
			if err != nil {
				mu.Unlock()
				continue
			}

			sess = &udpTargetSession{
				conn:     uConn,
				lastSeen: time.Now(),
			}
			targets[key] = sess

			// Listen for UDP replies from local service and forward back over channel
			go func(portNum uint16, sessID uint64, conn *net.UDPConn) {
				buf := make([]byte, 65535)
				for {
					_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
					n, err := conn.Read(buf)
					if err != nil {
						return
					}
					if n > 0 {
						mu.Lock()
						_ = writeUDPFrame(channel, portNum, sessID, buf[:n])
						mu.Unlock()
					}
				}
			}(port, sessionID, uConn)
		}

		sess.lastSeen = time.Now()
		mu.Unlock()

		_, _ = sess.conn.Write(data)
	}
}
