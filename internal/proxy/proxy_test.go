package proxy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/AmirAM03/velox/internal/engine"
	"github.com/AmirAM03/velox/internal/model"
)

type mockEngine struct {
	dialFunc func(ctx context.Context, cfg *model.ProxyConfig, network, addr string) (net.Conn, error)
}

func (m *mockEngine) Dial(ctx context.Context, cfg *model.ProxyConfig, network, addr string) (net.Conn, error) {
	if m.dialFunc != nil {
		return m.dialFunc(ctx, cfg, network, addr)
	}
	return (&net.Dialer{}).DialContext(ctx, network, addr)
}

func (m *mockEngine) Close() error {
	return nil
}

var _ engine.Engine = (*mockEngine)(nil)

// Failover walks the ranking in order and wraps around, so two dead nodes at
// the top cannot trap a profile between them.
func TestSelector_FailoverWalksRanking(t *testing.T) {
	a, b, c := &model.ProxyConfig{ID: "a"}, &model.ProxyConfig{ID: "b"}, &model.ProxyConfig{ID: "c"}
	sel := NewSelector(2, nil)
	if sel.RecordFailure("x") || sel.Active() != nil {
		t.Fatal("empty selector must not switch")
	}
	sel.SetPool([]*model.ProxyConfig{a, b, c})

	want := []string{"a", "b", "c", "a"}
	for i, id := range want {
		if got := sel.Active().ID; got != id {
			t.Fatalf("step %d: active %s, want %s", i, got, id)
		}
		if sel.RecordFailure("x") {
			t.Fatalf("step %d: switched after one failure, limit is 2", i)
		}
		if !sel.RecordFailure("x") {
			t.Fatalf("step %d: no switch at the limit", i)
		}
	}

	sel.RecordFailure("x")
	sel.RecordSuccess() // a success resets the count
	if sel.RecordFailure("x") {
		t.Error("switched although a success came between failures")
	}
}

func TestSelector_SetActive(t *testing.T) {
	a, b, c := &model.ProxyConfig{ID: "a"}, &model.ProxyConfig{ID: "b"}, &model.ProxyConfig{ID: "c"}
	sel := NewSelector(2, nil)
	sel.SetPool([]*model.ProxyConfig{a, b, c})

	if !sel.SetActive("c") || sel.Active().ID != "c" {
		t.Fatalf("SetActive(c) failed, active=%v", sel.Active())
	}
	if sel.SetActive("z") {
		t.Error("SetActive on a node outside the pool must return false")
	}
	if sel.Active().ID != "c" {
		t.Error("a failed SetActive must leave the active node unchanged")
	}
	// A manual switch resets the failure count, so the next failure does not flip nodes.
	sel.RecordFailure("x")
	sel.SetActive("a")
	if sel.RecordFailure("x") {
		t.Error("failure count should reset on a manual switch")
	}
}

func TestServer_SOCKS5_And_HTTP(t *testing.T) {
	// Start a backend echo HTTP server
	echoSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Echo", "pong")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("echo-response"))
	}))
	defer echoSrv.Close()

	// Mock engine that routes directly to destination
	eng := &mockEngine{
		dialFunc: func(ctx context.Context, cfg *model.ProxyConfig, network, addr string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}

	activeCfg := &model.ProxyConfig{ID: "active", Name: "Mock", Protocol: model.ProtocolVLESS}
	sel := NewSelector(2, nil)
	sel.SetPool([]*model.ProxyConfig{activeCfg})

	srv := NewServer("127.0.0.1", 0, "", "", eng, sel, nil)
	if err := srv.Start(); err != nil {
		t.Fatalf("start proxy server: %v", err)
	}
	defer srv.Stop()

	proxyAddr := srv.Addr().String()

	// 1. Test HTTP Proxy (GET)
	t.Run("HTTP_Proxy_GET", func(t *testing.T) {
		conn, err := net.Dial("tcp", proxyAddr)
		if err != nil {
			t.Fatalf("dial proxy: %v", err)
		}
		defer conn.Close()

		req := fmt.Sprintf("GET %s/test HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n",
			echoSrv.URL, echoSrv.Listener.Addr().String())
		_, err = conn.Write([]byte(req))
		if err != nil {
			t.Fatalf("write req: %v", err)
		}

		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			t.Fatalf("read response: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200 OK, got %d", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		if string(body) != "echo-response" {
			t.Errorf("expected body 'echo-response', got %q", string(body))
		}
	})

	// 2. Test HTTP CONNECT
	t.Run("HTTP_Proxy_CONNECT", func(t *testing.T) {
		conn, err := net.Dial("tcp", proxyAddr)
		if err != nil {
			t.Fatalf("dial proxy: %v", err)
		}
		defer conn.Close()

		connectReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n",
			echoSrv.Listener.Addr().String(), echoSrv.Listener.Addr().String())
		_, _ = conn.Write([]byte(connectReq))

		reader := bufio.NewReader(conn)
		statusLine, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read status: %v", err)
		}
		if statusLine != "HTTP/1.1 200 Connection Established\r\n" {
			t.Fatalf("unexpected status: %q", statusLine)
		}
	})

	// 3. Test SOCKS5 Connect
	t.Run("SOCKS5_Connect", func(t *testing.T) {
		conn, err := net.Dial("tcp", proxyAddr)
		if err != nil {
			t.Fatalf("dial proxy: %v", err)
		}
		defer conn.Close()

		// Handshake: [0x05, 1, 0x00] (no auth)
		_, _ = conn.Write([]byte{0x05, 0x01, 0x00})
		handshakeResp := make([]byte, 2)
		if _, err := io.ReadFull(conn, handshakeResp); err != nil {
			t.Fatalf("handshake read: %v", err)
		}
		if handshakeResp[0] != 0x05 || handshakeResp[1] != 0x00 {
			t.Fatalf("handshake failed: %v", handshakeResp)
		}

		// Connect request: domain name target
		host, portStr, _ := net.SplitHostPort(echoSrv.Listener.Addr().String())
		portNum, _ := strconv.Atoi(portStr)

		req := []byte{0x05, 0x01, 0x00, 0x03, byte(len(host))}
		req = append(req, []byte(host)...)
		req = append(req, byte(portNum>>8), byte(portNum&0xFF))

		_, _ = conn.Write(req)

		// Read reply (10 bytes for IPv4 bound)
		reply := make([]byte, 10)
		if _, err := io.ReadFull(conn, reply); err != nil {
			t.Fatalf("read reply: %v", err)
		}
		if reply[1] != 0x00 {
			t.Fatalf("socks5 connect reply error: %d", reply[1])
		}

		// Now write HTTP request through established SOCKS5 tunnel
		_, _ = conn.Write([]byte("GET /socks HTTP/1.1\r\nHost: test\r\nConnection: close\r\n\r\n"))
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			t.Fatalf("socks5 http read response: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200 OK through socks5, got %d", resp.StatusCode)
		}
	})
}
