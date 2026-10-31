package shadow

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

func TestShadow_TCPAndUDPEcho(t *testing.T) {
	// 1. TCP Target Echo Server (simulating Kharej local service on 18081)
	tcpTarget, err := net.Listen("tcp", "127.0.0.1:18081")
	if err != nil {
		t.Fatalf("Failed to start TCP target: %v", err)
	}
	defer tcpTarget.Close()

	go func() {
		for {
			conn, err := tcpTarget.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()

	// 2. UDP Target Echo Server (simulating WireGuard on 18082)
	uAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:18082")
	if err != nil {
		t.Fatalf("Failed to resolve UDP target: %v", err)
	}
	udpTarget, err := net.ListenUDP("udp", uAddr)
	if err != nil {
		t.Fatalf("Failed to start UDP target: %v", err)
	}
	defer udpTarget.Close()

	go func() {
		buf := make([]byte, 2048)
		for {
			n, rAddr, err := udpTarget.ReadFromUDP(buf)
			if err != nil {
				return
			}
			_, _ = udpTarget.WriteToUDP(buf[:n], rAddr)
		}
	}()

	// 3. Kharej Shadow Server on :13080
	server, err := StartServer("127.0.0.1:13080", "secret-token", "./test_shadow_key")
	if err != nil {
		t.Fatalf("Failed to start shadow server: %v", err)
	}
	defer server.Close()

	// 4. Iran Shadow Client forwarding port 18081 (TCP) and 18082 (UDP)
	// To avoid collision on localhost:
	// We test client connection and direct channel dial!
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Direct client test via ssh.Dial
	time.Sleep(100 * time.Millisecond)
	cli := StartClient(ctx, "127.0.0.1", 13080, "secret-token", []string{"18083"})
	defer cli.Close()

	// Wait for connection
	for i := 0; i < 20; i++ {
		if cli.IsConnected() {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !cli.IsConnected() {
		t.Fatalf("Shadow client failed to connect: %s", cli.LastErr())
	}

	// Test TCP channel forward through ssh client
	cli.mu.Lock()
	sshC := cli.sshClient
	cli.mu.Unlock()

	conn, err := sshC.Dial("tcp", "127.0.0.1:18081")
	if err != nil {
		t.Fatalf("Failed to dial TCP target over SSH channel: %v", err)
	}
	defer conn.Close()

	msg := []byte("hello-nora-shadow-tcp")
	if _, err := conn.Write(msg); err != nil {
		t.Fatalf("Failed to write to channel: %v", err)
	}

	recv := make([]byte, len(msg))
	if _, err := io.ReadFull(conn, recv); err != nil {
		t.Fatalf("Failed to read from channel: %v", err)
	}

	if string(recv) != string(msg) {
		t.Fatalf("Echo mismatch: got %s, want %s", string(recv), string(msg))
	}

	// 6. Test UDP frame forwarding through nora-udp channel
	cli.udpChanMu.Lock()
	uChan := cli.udpChan
	cli.udpChanMu.Unlock()

	if uChan == nil {
		t.Fatalf("UDP channel is nil")
	}

	udpMsg := []byte("wireguard-handshake-ping")
	sessionID := uint64(9999)
	port := uint16(18082)

	cli.udpChanMu.Lock()
	err = writeUDPFrame(uChan, port, sessionID, udpMsg)
	cli.udpChanMu.Unlock()
	if err != nil {
		t.Fatalf("Failed to write UDP frame: %v", err)
	}

	// Wait for echo frame
	readDone := make(chan bool)
	go func() {
		rPort, rSess, rData, err := readUDPFrame(uChan)
		if err != nil {
			t.Errorf("readUDPFrame error: %v", err)
			return
		}
		if rPort != port || rSess != sessionID || string(rData) != string(udpMsg) {
			t.Errorf("UDP frame mismatch: got port=%d sess=%d data=%s", rPort, rSess, string(rData))
			return
		}
		readDone <- true
	}()

	select {
	case <-readDone:
		// success
	case <-time.After(2 * time.Second):
		t.Fatalf("Timed out waiting for UDP echo frame")
	}
}
