package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AmirAM03/velox/internal/config"
	"github.com/AmirAM03/velox/internal/engine"
	"github.com/AmirAM03/velox/internal/ingest"
	"github.com/AmirAM03/velox/internal/model"
	"github.com/AmirAM03/velox/internal/parser"
	"github.com/AmirAM03/velox/internal/proxy"
	"github.com/AmirAM03/velox/internal/storage"
	"github.com/AmirAM03/velox/internal/system"
)

// Server manages the web dashboard HTTP server and background proxy state.
type Server struct {
	cfg    *config.Config
	store  *storage.Store
	logger *slog.Logger
	port   int
	jobMgr *BenchmarkJobManager

	mu             sync.Mutex
	proxyEngine    engine.Engine
	proxyServer    *proxy.Server
	proxySelector  *proxy.Selector
	activeConfig   *model.ProxyConfig
	sysProxyActive bool
}

// NewServer creates a new web dashboard server.
func NewServer(cfg *config.Config, store *storage.Store, port int, logger *slog.Logger) *Server {
	if port <= 0 {
		port = 18080
	}
	if logger == nil {
		logger = slog.Default()
	}

	jobMgr := NewBenchmarkJobManager(store, &cfg.Pipeline, cfg.Scoring, logger)

	return &Server{
		cfg:    cfg,
		store:  store,
		logger: logger,
		port:   port,
		jobMgr: jobMgr,
	}
}

// Start runs the HTTP server and blocks until ctx is canceled.
func (s *Server) Start(ctx context.Context, openBrowser bool) error {
	mux := http.NewServeMux()

	// 1. Static file serving from embedded FS
	subFS, err := fs.Sub(StaticFS, "static")
	if err != nil {
		return fmt.Errorf("sub embedded static fs: %w", err)
	}
	fileServer := http.FileServer(http.FS(subFS))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			data, err := fs.ReadFile(subFS, "index.html")
			if err != nil {
				http.Error(w, "index.html not found", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(data)
			return
		}
		http.StripPrefix("/static/", fileServer).ServeHTTP(w, r)
	})
	mux.Handle("/static/", http.StripPrefix("/static/", fileServer))

	// 2. REST API endpoints
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/configs", s.handleConfigs)
	mux.HandleFunc("/api/parse", s.handleParse)
	mux.HandleFunc("/api/test", s.handleTest)
	mux.HandleFunc("/api/test/stream", s.handleTestStream)
	mux.HandleFunc("/api/test/cancel", s.handleTestCancel)
	mux.HandleFunc("/api/connect", s.handleConnect)
	mux.HandleFunc("/api/disconnect", s.handleDisconnect)
	mux.HandleFunc("/api/system-proxy", s.handleSystemProxy)
	mux.HandleFunc("/api/dedup", s.handleDedup)

	addr := fmt.Sprintf("127.0.0.1:%d", s.port)
	httpSrv := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		s.Close()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		httpSrv.Shutdown(shutdownCtx)
	}()

	url := fmt.Sprintf("http://%s", addr)
	s.logger.Info("⚡ Velox Web Dashboard listening", "url", url)
	fmt.Printf("\n⚡ Velox Web Dashboard is running at: %s\n", url)
	fmt.Println("Press Ctrl+C in this terminal to stop.")

	if openBrowser {
		go openURL(url)
	}

	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("http server: %w", err)
	}
	return nil
}

// Close gracefully stops the proxy and resets system proxy.
func (s *Server) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.sysProxyActive {
		sysProxy := system.NewProxyController(s.logger)
		_ = sysProxy.Disable()
		s.sysProxyActive = false
	}
	if s.proxyServer != nil {
		s.proxyServer.Stop()
		s.proxyServer = nil
	}
	if s.proxyEngine != nil {
		s.proxyEngine.Close()
		s.proxyEngine = nil
	}
	s.activeConfig = nil
}

// API: Status
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	total, _ := s.store.ConfigCount()
	protoCounts, _ := s.store.ConfigCountByProtocol()

	s.mu.Lock()
	var active *model.ProxyConfig
	if s.activeConfig != nil {
		active = s.activeConfig
	}
	sysProxy := s.sysProxyActive
	s.mu.Unlock()

	resp := map[string]interface{}{
		"total_configs":        total,
		"protocol_counts":      protoCounts,
		"active_config":        active,
		"system_proxy_enabled": sysProxy,
		"mixed_port":           s.cfg.Proxy.MixedPort,
		"db_path":              s.cfg.DBPath(),
	}

	writeJSON(w, http.StatusOK, resp)
}

// API: Configs
func (s *Server) handleConfigs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	protocol := q.Get("protocol")
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	search := strings.ToLower(strings.TrimSpace(q.Get("search")))

	configs, err := s.store.ListConfigsFiltered(protocol, limit+offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if search != "" {
		var matched []*model.ProxyConfig
		for _, cfg := range configs {
			if strings.Contains(strings.ToLower(cfg.Name), search) ||
				strings.Contains(strings.ToLower(cfg.Address), search) ||
				strings.Contains(strconv.Itoa(cfg.Port), search) {
				matched = append(matched, cfg)
			}
		}
		configs = matched
	}

	total := len(configs)
	if offset > len(configs) {
		configs = nil
	} else {
		end := offset + limit
		if end > len(configs) {
			end = len(configs)
		}
		configs = configs[offset:end]
	}

	type configItem struct {
		ID        string   `json:"id"`
		Name      string   `json:"name"`
		Protocol  string   `json:"protocol"`
		Address   string   `json:"address"`
		Port      int      `json:"port"`
		Network   string   `json:"network"`
		Security  string   `json:"security"`
		Score     *float64 `json:"score,omitempty"`
		LatencyMS *float64 `json:"latency_ms,omitempty"`
		RawURI    string   `json:"raw_uri"`
	}

	var items []configItem
	for _, cfg := range configs {
		item := configItem{
			ID:       cfg.ID,
			Name:     cfg.DisplayName(),
			Protocol: string(cfg.Protocol),
			Address:  cfg.Address,
			Port:     cfg.Port,
			Network:  string(cfg.Network),
			Security: string(cfg.Security),
			RawURI:   cfg.RawURI,
		}
		score, _ := s.store.GetScore(cfg.ID)
		if score != nil {
			item.Score = &score.Composite
			lat := float64(score.LatencyScore)
			item.LatencyMS = &lat
		}
		items = append(items, item)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"configs": items,
		"total":   total,
	})
}

// API: Parse / Ingest
func (s *Server) handleParse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		URLs []string `json:"urls"`
		Raw  string   `json:"raw"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var allRaw []string
	for _, u := range req.URLs {
		s.jobMgr.Log("info", "ingest", fmt.Sprintf("Fetching subscription: %s", u))
		raw, err := ingest.FetchSubscription(u, 0)
		if err != nil {
			s.logger.Error("web ingest: failed fetch", "url", u, "error", err)
			s.jobMgr.Log("error", "ingest", fmt.Sprintf("Failed to fetch %s: %v", u, err))
			continue
		}
		s.jobMgr.Log("success", "ingest", fmt.Sprintf("Successfully retrieved payload from %s (%d bytes)", u, len(raw)))
		allRaw = append(allRaw, raw)
	}
	if req.Raw != "" {
		s.jobMgr.Log("info", "ingest", fmt.Sprintf("Processing manual text input (%d bytes)", len(req.Raw)))
		allRaw = append(allRaw, req.Raw)
	}

	merged := strings.Join(allRaw, "\n")
	cleaned := ingest.CleanRawContent(merged)

	configs, failures, batchDups := parser.ParseManyDetailed(cleaned)
	for _, cfg := range configs {
		cfg.Source = "web-ui"
	}
	s.jobMgr.Log("info", "ingest", fmt.Sprintf("Parsed %d configurations (%d duplicates discarded, %d invalid lines skipped)", len(configs), batchDups, failures))

	inserted, updated, err := s.store.UpsertConfigs(configs)
	if err != nil {
		s.jobMgr.Log("error", "ingest", fmt.Sprintf("Database upsert failed: %v", err))
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.jobMgr.Log("success", "ingest", fmt.Sprintf("Database updated: %d new inserted, %d existing refreshed", inserted, updated))

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"parsed":           len(configs),
		"inserted":         inserted,
		"updated":          updated,
		"batch_duplicates": batchDups,
		"failures":         failures,
	})
}

// API: Test / Benchmark (Starts asynchronous test runner)
func (s *Server) handleTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		Target   string `json:"target"`
		Threads  int    `json:"threads"`
		Protocol string `json:"protocol"`
		Scope    string `json:"scope"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.Target = strings.TrimSpace(req.Target)
	if req.Target == "" {
		req.Target = "https://www.google.com/generate_204"
	}
	if !strings.HasPrefix(req.Target, "http://") && !strings.HasPrefix(req.Target, "https://") {
		req.Target = "https://" + req.Target
	}
	if req.Threads <= 0 {
		req.Threads = 100
	} else if req.Threads > 1000 {
		req.Threads = 1000
	}

	protoFilter := ""
	if req.Protocol != "" && req.Protocol != "all" {
		protoFilter = strings.ToLower(req.Protocol)
	}

	var configs []*model.ProxyConfig
	var err error

	switch req.Scope {
	case "untested":
		configs, err = s.store.ListConfigsUntested(protoFilter, 0)
	case "top100":
		configs, err = s.store.ListConfigsFiltered(protoFilter, 100)
	case "top500":
		configs, err = s.store.ListConfigsFiltered(protoFilter, 500)
	default:
		// "all" or empty: benchmark all matching configs in database
		configs, err = s.store.ListConfigsFiltered(protoFilter, 0)
	}

	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if len(configs) == 0 {
		writeError(w, http.StatusBadRequest, "no configurations match selection to test")
		return
	}

	if err := s.jobMgr.Start(req.Target, req.Threads, configs); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":   "started",
		"target":   req.Target,
		"threads":  req.Threads,
		"total":    len(configs),
		"protocol": req.Protocol,
		"scope":    req.Scope,
	})
}

// API: Test Stream (Server-Sent Events)
func (s *Server) handleTestStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ch := s.jobMgr.Subscribe()
	defer s.jobMgr.Unsubscribe(ch)

	flusher.Flush()

	notify := r.Context().Done()
	for {
		select {
		case <-notify:
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			w.Write(msg)
			flusher.Flush()
		}
	}
}

// API: Cancel Test
func (s *Server) handleTestCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if s.jobMgr.Cancel() {
		writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
	} else {
		writeJSON(w, http.StatusOK, map[string]string{"status": "not_running"})
	}
}

// API: Connect
func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		ConfigID string `json:"config_id"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	var targetConfig *model.ProxyConfig
	if req.ConfigID != "" {
		configs, err := s.store.GetConfigsByIDs([]string{req.ConfigID})
		if err != nil || len(configs) == 0 {
			writeError(w, http.StatusNotFound, "config not found")
			return
		}
		targetConfig = configs[0]
	} else {
		configs, err := s.store.ListConfigs(1)
		if err != nil || len(configs) == 0 {
			writeError(w, http.StatusBadRequest, "no configurations available")
			return
		}
		targetConfig = configs[0]
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Stop previous proxy if running
	if s.proxyServer != nil {
		s.proxyServer.Stop()
		s.proxyServer = nil
	}
	if s.proxyEngine != nil {
		s.proxyEngine.Close()
		s.proxyEngine = nil
	}

	// Initialize Engine
	eng := engine.NewXrayEngine(s.logger)
	sel := proxy.NewSelector(s.store, 5, 2, s.logger)
	sel.SelectExplicitly(targetConfig)

	mixedPort := s.cfg.Proxy.MixedPort
	if mixedPort <= 0 {
		mixedPort = 1080
	}

	s.jobMgr.Log("info", "proxy", fmt.Sprintf("Activating local proxy routing via '%s' (%s:%d)", targetConfig.DisplayName(), targetConfig.Address, targetConfig.Port))

	srv := proxy.NewServer(s.cfg.Proxy.ListenAddr, mixedPort, eng, sel, s.logger)
	if err := srv.Start(); err != nil {
		eng.Close()
		s.jobMgr.Log("error", "proxy", fmt.Sprintf("Failed to bind proxy on %s:%d: %v", s.cfg.Proxy.ListenAddr, mixedPort, err))
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("start proxy: %v", err))
		return
	}

	s.proxyEngine = eng
	s.proxyServer = srv
	s.proxySelector = sel
	s.activeConfig = targetConfig

	s.jobMgr.Log("success", "proxy", fmt.Sprintf("Mixed proxy (HTTP/SOCKS5) listening on %s:%d", s.cfg.Proxy.ListenAddr, mixedPort))

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":   "connected",
		"id":       targetConfig.ID,
		"name":     targetConfig.DisplayName(),
		"protocol": string(targetConfig.Protocol),
		"port":     mixedPort,
	})
}

// API: Disconnect
func (s *Server) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	s.Close()
	s.jobMgr.Log("info", "proxy", "Local proxy disconnected and shut down")
	writeJSON(w, http.StatusOK, map[string]string{"status": "disconnected"})
}

// API: Toggle System Proxy
func (s *Server) handleSystemProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	port := s.cfg.Proxy.MixedPort
	if port <= 0 {
		port = 1080
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	sysProxy := system.NewProxyController(s.logger)
	if req.Enabled {
		if err := sysProxy.Enable(s.cfg.Proxy.ListenAddr, port, port); err != nil {
			s.jobMgr.Log("error", "system", fmt.Sprintf("Failed to enable system proxy: %v", err))
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("set system proxy: %v", err))
			return
		}
		s.sysProxyActive = true
		s.jobMgr.Log("success", "system", fmt.Sprintf("OS system proxy redirected to %s:%d", s.cfg.Proxy.ListenAddr, port))
	} else {
		if err := sysProxy.Disable(); err != nil {
			s.jobMgr.Log("error", "system", fmt.Sprintf("Failed to disable system proxy: %v", err))
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("clear system proxy: %v", err))
			return
		}
		s.sysProxyActive = false
		s.jobMgr.Log("info", "system", "OS system proxy cleared and disabled")
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"enabled": s.sysProxyActive,
	})
}

// API: Deduplicate
func (s *Server) handleDedup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	s.jobMgr.Log("info", "dedup", "Starting database deduplication pass across stored configurations...")
	scanned, removed, err := s.store.Deduplicate()
	if err != nil {
		s.jobMgr.Log("error", "dedup", fmt.Sprintf("Deduplication error: %v", err))
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.jobMgr.Log("success", "dedup", fmt.Sprintf("Deduplication complete: %d records scanned, %d duplicates removed", scanned, removed))

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"scanned": scanned,
		"removed": removed,
	})
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func openURL(url string) {
	time.Sleep(200 * time.Millisecond)
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	cmd.Start()
}
