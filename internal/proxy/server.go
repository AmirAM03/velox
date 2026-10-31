package proxy

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/base64"
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
	username   string // empty means no auth (tests and loopback only)
	password   string
	engine     engine.Engine
	selector   *Selector
	logger     *slog.Logger
	listener   net.Listener
	mu         sync.Mutex
	running    bool
	done       chan struct{}
}

// NewServer creates a new mixed inbound proxy Server. With a username set, clients
// must authenticate (SOCKS5 RFC 1929 or HTTP Proxy-Authorization: Basic).
func NewServer(listenAddr string, port int, username, password string, eng engine.Engine, sel *Selector, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	if listenAddr == "" {
		listenAddr = "127.0.0.1"
	}
	return &Server{
		listenAddr: listenAddr,
		port:       port,
		username:   username,
		password:   password,
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
				time.Sleep(50 * time.Millisecond) // avoid a hot loop on persistent errors such as EMFILE
				continue
			}
		}

		go s.handleConn(conn)
	}
}

// handleConn inspects the first byte to dispatch between SOCKS5 and HTTP proxy.
func (s *Server) handleConn(clientConn net.Conn) {
	defer clientConn.Close()

	// The whole handshake must finish in time; relay() clears the deadline.
	// Without it, idle connections on a public port would pile up.
	_ = clientConn.SetReadDeadline(time.Now().Add(15 * time.Second))
	reader := bufio.NewReader(clientConn)
	firstByte, err := reader.Peek(1)
	if err != nil {
		return
	}

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

	// 2. Pick the auth method: username/password (0x02) when configured, else none (0x00)
	if s.username != "" {
		if !bytesContain(methods, 0x02) {
			_, _ = clientConn.Write([]byte{0x05, 0xFF})
			return
		}
		if _, err := clientConn.Write([]byte{0x05, 0x02}); err != nil {
			return
		}
		if !s.socksAuth(clientConn, reader) {
			return
		}
	} else if _, err := clientConn.Write([]byte{0x05, 0x00}); err != nil {
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
		s.selector.RecordFailure(err.Error())
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

	if s.username != "" && !s.httpAuthOK(req.Header.Get("Proxy-Authorization")) {
		_, _ = clientConn.Write([]byte("HTTP/1.1 407 Proxy Authentication Required\r\nProxy-Authenticate: Basic realm=\"velox\"\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"))
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
			s.selector.RecordFailure(err.Error())
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
			s.selector.RecordFailure(err.Error())
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
		// One request per connection: the raw relay below cannot re-route a reused
		// keep-alive connection when the browser's next request is for another host.
		req.Close = true

		if err := req.Write(upstreamConn); err != nil {
			return
		}

		// Relay response and any subsequent pipelined traffic
		s.relay(clientConn, reader, upstreamConn)
	}
}

// relay copies data bidirectionally between client and upstream.
func (s *Server) relay(clientConn net.Conn, clientReader *bufio.Reader, upstreamConn net.Conn) {
	s.selector.RecordSuccess()
	_ = clientConn.SetReadDeadline(time.Time{}) // handshake done; long-lived tunnels are fine now
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

// socksAuth runs the RFC 1929 username/password sub-negotiation.
func (s *Server) socksAuth(conn net.Conn, r *bufio.Reader) bool {
	ver, err := r.ReadByte()
	if err != nil || ver != 0x01 {
		return false
	}
	read := func() ([]byte, bool) {
		n, err := r.ReadByte()
		if err != nil {
			return nil, false
		}
		b := make([]byte, n)
		_, err = io.ReadFull(r, b)
		return b, err == nil
	}
	user, ok1 := read()
	pass, ok2 := read()
	ok := ok1 && ok2 && s.credsOK(string(user), string(pass))
	status := byte(0x00)
	if !ok {
		status = 0x01
	}
	_, _ = conn.Write([]byte{0x01, status})
	return ok
}

// httpAuthOK checks a "Basic base64(user:pass)" Proxy-Authorization header.
func (s *Server) httpAuthOK(header string) bool {
	const prefix = "Basic "
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(header, prefix))
	if err != nil {
		return false
	}
	user, pass, ok := strings.Cut(string(raw), ":")
	return ok && s.credsOK(user, pass)
}

func (s *Server) credsOK(user, pass string) bool {
	u := subtle.ConstantTimeCompare([]byte(user), []byte(s.username))
	p := subtle.ConstantTimeCompare([]byte(pass), []byte(s.password))
	return u&p == 1
}

func bytesContain(b []byte, v byte) bool {
	for _, x := range b {
		if x == v {
			return true
		}
	}
	return false
}
