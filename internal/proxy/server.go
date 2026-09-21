package proxy

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AmirAM03/velox/internal/engine"
)

// Server is a mixed SOCKS5 and HTTP inbound proxy server.
type Server struct {
	listenAddr string
	port       int
	engine     engine.Engine
	selector   *Selector
	logger     *slog.Logger
	listener   net.Listener
	mu         sync.Mutex
	running    bool
	done       chan struct{}
}

// NewServer creates a new mixed inbound proxy Server.
func NewServer(listenAddr string, port int, eng engine.Engine, sel *Selector, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	if listenAddr == "" {
		listenAddr = "127.0.0.1"
	}
	return &Server{
		listenAddr: listenAddr,
		port:       port,
		engine:     eng,
		selector:   sel,
		logger:     logger,
		done:       make(chan struct{}),
	}
}

// Start begins listening on the configured address and port.
func (s *Server) Start() error {
	addr := fmt.Sprintf("%s:%d", s.listenAddr, s.port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}

	s.mu.Lock()
	s.listener = ln
	s.running = true
	s.mu.Unlock()

	s.logger.Info("local mixed proxy server started",
		"address", addr,
		"protocols", "SOCKS5 / HTTP / HTTPS-CONNECT",
	)

	go s.acceptLoop()
	return nil
}

// Addr returns the listener's network address, or nil if not listening.
func (s *Server) Addr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil {
		return s.listener.Addr()
	}
	return nil
}

// Stop stops the proxy server and closes the listener.
func (s *Server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return nil
	}

	s.running = false
	close(s.done)

	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
				s.logger.Debug("accept error", "error", err)
				continue
			}
		}

		go s.handleConn(conn)
	}
}

// handleConn inspects the first byte to dispatch between SOCKS5 and HTTP proxy.
func (s *Server) handleConn(clientConn net.Conn) {
	defer clientConn.Close()

	// Read first byte with timeout
	_ = clientConn.SetReadDeadline(time.Now().Add(10 * time.Second))
	reader := bufio.NewReader(clientConn)
	firstByte, err := reader.Peek(1)
	if err != nil {
		return
	}
	_ = clientConn.SetReadDeadline(time.Time{}) // Clear deadline

	if firstByte[0] == 0x05 {
		// SOCKS5 protocol
		s.handleSOCKS5(clientConn, reader)
	} else {
		// HTTP proxy protocol (CONNECT or standard GET/POST)
		s.handleHTTP(clientConn, reader)
	}
}

// handleSOCKS5 handles a SOCKS5 client connection.
func (s *Server) handleSOCKS5(clientConn net.Conn, reader *bufio.Reader) {
	// 1. Read version and method count
	ver, err := reader.ReadByte()
	if err != nil || ver != 0x05 {
		return
	}

	nMethods, err := reader.ReadByte()
	if err != nil {
		return
	}

	methods := make([]byte, nMethods)
	if _, err := io.ReadFull(reader, methods); err != nil {
		return
	}

	// 2. Select No Authentication Required (0x00)
	if _, err := clientConn.Write([]byte{0x05, 0x00}); err != nil {
		return
	}

	// 3. Read request
	header := make([]byte, 4)
	if _, err := io.ReadFull(reader, header); err != nil {
		return
	}

	cmd := header[1]
	if cmd != 0x01 { // 0x01 = CONNECT
		// Command not supported
		_, _ = clientConn.Write([]byte{0x05, 0x07, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}

	atyp := header[3]
	var destHost string

	switch atyp {
	case 0x01: // IPv4
		ip := make([]byte, 4)
		if _, err := io.ReadFull(reader, ip); err != nil {
			return
		}
		destHost = net.IP(ip).String()

	case 0x03: // Domain name
		length, err := reader.ReadByte()
		if err != nil {
			return
		}
		domain := make([]byte, length)
		if _, err := io.ReadFull(reader, domain); err != nil {
			return
		}
		destHost = string(domain)

	case 0x04: // IPv6
		ip := make([]byte, 16)
		if _, err := io.ReadFull(reader, ip); err != nil {
			return
		}
		destHost = net.IP(ip).String()

	default:
		// Address type not supported
		_, _ = clientConn.Write([]byte{0x05, 0x08, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}

	var portBytes [2]byte
	if _, err := io.ReadFull(reader, portBytes[:]); err != nil {
		return
	}
	destPort := binary.BigEndian.Uint16(portBytes[:])
	destAddr := net.JoinHostPort(destHost, strconv.Itoa(int(destPort)))

	// 4. Dial upstream proxy
	activeCfg := s.selector.Active()
	if activeCfg == nil {
		_, _ = clientConn.Write([]byte{0x05, 0x01, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		s.logger.Warn("socks5 reject: no active proxy config")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	upstreamConn, err := s.engine.Dial(ctx, activeCfg, "tcp", destAddr)
	if err != nil {
		// General SOCKS server failure
		_, _ = clientConn.Write([]byte{0x05, 0x01, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		s.logger.Debug("socks5 dial upstream error", "dest", destAddr, "error", err)
		return
	}
	defer upstreamConn.Close()

	// 5. Send success response
	if _, err := clientConn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}

	// 6. Splice traffic
	s.relay(clientConn, reader, upstreamConn)
}

// handleHTTP handles HTTP CONNECT (HTTPS tunneling) or plain HTTP proxy requests.
func (s *Server) handleHTTP(clientConn net.Conn, reader *bufio.Reader) {
	req, err := http.ReadRequest(reader)
	if err != nil {
		return
	}

	activeCfg := s.selector.Active()
	if activeCfg == nil {
		resp := "HTTP/1.1 503 Service Unavailable\r\nContent-Type: text/plain\r\n\r\nNo active proxy config"
		_, _ = clientConn.Write([]byte(resp))
		return
	}

	if req.Method == http.MethodConnect {
		// HTTPS Tunnel: CONNECT host:port HTTP/1.1
		destAddr := req.RequestURI
		if !strings.Contains(destAddr, ":") {
			destAddr = net.JoinHostPort(destAddr, "443")
		}

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		upstreamConn, err := s.engine.Dial(ctx, activeCfg, "tcp", destAddr)
		if err != nil {
			resp := fmt.Sprintf("HTTP/1.1 502 Bad Gateway\r\n\r\nProxy error: %v", err)
			_, _ = clientConn.Write([]byte(resp))
			return
		}
		defer upstreamConn.Close()

		// Send 200 Connection Established
		_, err = clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
		if err != nil {
			return
		}

		// Relay bidirectional bytes
		s.relay(clientConn, reader, upstreamConn)
	} else {
		// Plain HTTP proxy request: GET http://example.com/path HTTP/1.1
		destHost := req.URL.Hostname()
		destPort := req.URL.Port()
		if destPort == "" {
			destPort = "80"
		}
		destAddr := net.JoinHostPort(destHost, destPort)

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		upstreamConn, err := s.engine.Dial(ctx, activeCfg, "tcp", destAddr)
		if err != nil {
			resp := fmt.Sprintf("HTTP/1.1 502 Bad Gateway\r\n\r\nProxy error: %v", err)
			_, _ = clientConn.Write([]byte(resp))
			return
		}
		defer upstreamConn.Close()

		// Write modified request to upstream
		req.RequestURI = ""
		req.Header.Del("Proxy-Connection")
		req.Header.Del("Proxy-Authenticate")
		req.Header.Del("Proxy-Authorization")

		if err := req.Write(upstreamConn); err != nil {
			return
		}

		// Relay response and any subsequent pipelined traffic
		s.relay(clientConn, reader, upstreamConn)
	}
}

// relay copies data bidirectionally between client and upstream.
func (s *Server) relay(clientConn net.Conn, clientReader *bufio.Reader, upstreamConn net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)

	// Client -> Upstream
	go func() {
		defer wg.Done()
		defer upstreamConn.Close()

		// First flush any buffered bytes in bufio.Reader
		if clientReader.Buffered() > 0 {
			buffered := make([]byte, clientReader.Buffered())
			n, _ := clientReader.Read(buffered)
			if n > 0 {
				if _, err := upstreamConn.Write(buffered[:n]); err != nil {
					return
				}
			}
		}

		_, _ = io.Copy(upstreamConn, clientConn)
	}()

	// Upstream -> Client
	go func() {
		defer wg.Done()
		defer clientConn.Close()
		_, _ = io.Copy(clientConn, upstreamConn)
	}()

	wg.Wait()
}
