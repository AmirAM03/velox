package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/AmirAM03/velox/internal/config"
	loggerPkg "github.com/AmirAM03/velox/internal/logger"
	"github.com/AmirAM03/velox/internal/model"
	"github.com/AmirAM03/velox/internal/storage"
)

// Server manages the web dashboard HTTP server and background proxy state.
type Server struct {
	cfg         *config.Config
	store       *storage.Store
	logger      *slog.Logger
	dbHandler   *loggerPkg.DBHandler
	port        int
	jobMgr      *BenchmarkJobManager
	profiles    *ProfileManager
	optimizer   *Optimizer
}

// NewServer creates a new web dashboard server.
func NewServer(cfg *config.Config, store *storage.Store, port int, logger *slog.Logger, dbHandler *loggerPkg.DBHandler) *Server {
	if port <= 0 {
		port = 18080
	}
	if logger == nil {
		logger = slog.Default()
	}

	jobMgr := NewBenchmarkJobManager(store, &cfg.Pipeline, cfg.Scoring, logger)

	if dbHandler != nil {
		dbHandler.AddBroadcast(func(rec *storage.LogRecord) {
			jobMgr.BroadcastAppLog(rec)
		})
	}

	s := &Server{
		cfg:       cfg,
		store:     store,
		logger:    logger,
		dbHandler: dbHandler,
		port:      port,
		jobMgr:    jobMgr,
	}

	s.profiles = NewProfileManager(store, cfg.Proxy.ListenAddr, cfg.Proxy.FailThreshold, logger)
	s.optimizer = NewOptimizer(store, &cfg.Pipeline, s.profiles, logger, jobMgr.Broadcast, jobMgr.Running)

	return s
}

// Start runs the HTTP server and blocks until ctx is canceled.
func (s *Server) Start(ctx context.Context) error {
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
	mux.HandleFunc("/api/test", s.handleTest)
	mux.HandleFunc("/api/test/stream", s.handleTestStream)
	mux.HandleFunc("/api/test/cancel", s.handleTestCancel)
	mux.HandleFunc("/api/dedup", s.handleDedup)

	// Application Logs API
	mux.HandleFunc("/api/logs", s.handleLogs)
	mux.HandleFunc("/api/logs/stats", s.handleLogStats)
	mux.HandleFunc("/api/logs/retention", s.handleLogRetention)
	mux.HandleFunc("/api/logs/prune", s.handleLogPrune)
	mux.HandleFunc("/api/logs/clear", s.handleLogClear)
	mux.HandleFunc("/api/logs/export", s.handleLogExport)

	// Subscription sources, profiles and the background optimizer
	mux.HandleFunc("/api/sources", s.handleSources)
	mux.HandleFunc("/api/sources/toggle", s.handleSourceToggle)
	mux.HandleFunc("/api/sources/delete", s.handleSourceDelete)
	mux.HandleFunc("/api/profiles", s.handleProfiles)
	mux.HandleFunc("/api/profiles/delete", s.handleProfileDelete)
	mux.HandleFunc("/api/optimizer", s.handleOptimizer)
	mux.HandleFunc("/api/optimizer/run", s.handleOptimizerRun)

	// Benchmark Methodology Chain API
	mux.HandleFunc("/api/benchmark/methodology", s.handleBenchmarkMethodology)

	// Start profile listeners and the background optimizer
	if err := s.profiles.Sync(); err != nil {
		return err
	}
	s.optimizer.Start(ctx)

	addr := fmt.Sprintf("127.0.0.1:%d", s.port)
	httpSrv := &http.Server{
		Addr:    addr,
		Handler: localOnly(mux),
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

	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("http server: %w", err)
	}
	return nil
}

// Close stops all profile listeners.
func (s *Server) Close() {
	s.profiles.Close()
}

// API: Status
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	total, _ := s.store.ConfigCount()
	protoCounts, _ := s.store.ConfigCountByProtocol()

	profiles, _ := s.profiles.Status()
	sources, _ := s.store.ListSources()

	resp := map[string]interface{}{
		"total_configs":   total,
		"protocol_counts": protoCounts,
		"db_path":         s.cfg.DBPath(),
		"listen_addr":     s.cfg.Proxy.ListenAddr,
		"profiles":        profiles,
		"sources":         sources,
		"optimizer":       s.optimizer.Status(),
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
	search := strings.TrimSpace(q.Get("search"))
	workingOnly := q.Get("working_only") == "true" || q.Get("working_only") == "1"
	sortBy := q.Get("sort_by")
	sortOrder := q.Get("sort_order")

	filter := storage.ConfigQueryFilter{
		Protocol:    protocol,
		Search:      search,
		WorkingOnly: workingOnly,
		SortBy:      sortBy,
		SortOrder:   sortOrder,
		Limit:       limit,
		Offset:      offset,
	}

	items, total, err := s.store.QueryConfigs(filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"configs":      items,
		"total":        total,
		"limit":        limit,
		"offset":       offset,
		"working_only": workingOnly,
		"sort_by":      sortBy,
		"sort_order":   sortOrder,
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

// API: Get Logs
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 100
	}
	offset, _ := strconv.Atoi(q.Get("offset"))

	filter := storage.LogQueryFilter{
		Level:  q.Get("level"),
		Source: q.Get("source"),
		Search: q.Get("search"),
		Limit:  limit,
		Offset: offset,
	}

	if sinceStr := q.Get("since"); sinceStr != "" {
		if t, err := time.Parse(time.RFC3339, sinceStr); err == nil {
			filter.Since = t
		}
	}
	if untilStr := q.Get("until"); untilStr != "" {
		if t, err := time.Parse(time.RFC3339, untilStr); err == nil {
			filter.Until = t
		}
	}

	records, total, err := s.store.QueryLogs(filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if records == nil {
		records = []*storage.LogRecord{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"logs":   records,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// API: Get Log Stats
func (s *Server) handleLogStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	stats, err := s.store.GetLogStats()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, stats)
}

// API: Update Log Retention
func (s *Server) handleLogRetention(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		Retention string `json:"retention"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.Retention = strings.TrimSpace(req.Retention)
	dur, err := loggerPkg.ParseRetention(req.Retention)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid retention policy: %v", err))
		return
	}

	if err := s.store.SetSetting("log_retention", req.Retention); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("save retention: %v", err))
		return
	}

	pruned, _ := s.store.PruneLogs(dur)
	s.logger.Info("application log retention policy updated",
		slog.String("source", "system"),
		slog.String("retention", req.Retention),
		slog.Int64("pruned_expired", pruned),
	)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":   true,
		"retention": req.Retention,
		"pruned":    pruned,
	})
}

// API: Manually Prune Logs
func (s *Server) handleLogPrune(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		Duration string `json:"duration"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	retentionStr := req.Duration
	if retentionStr == "" {
		retentionStr, _ = s.store.GetSetting("log_retention", "7d")
	}

	dur, err := loggerPkg.ParseRetention(retentionStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid duration: %v", err))
		return
	}

	pruned, err := s.store.PruneLogs(dur)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("prune logs: %v", err))
		return
	}

	s.logger.Info("manual application log prune triggered",
		slog.String("source", "system"),
		slog.Int64("pruned_records", pruned),
		slog.String("retention", retentionStr),
	)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"pruned":  pruned,
	})
}

// API: Clear All Logs
func (s *Server) handleLogClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if err := s.store.ClearLogs(); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("clear logs: %v", err))
		return
	}

	s.logger.Info("application logs cleared and database vacuumed", slog.String("source", "system"))

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
	})
}

// API: Export Logs
func (s *Server) handleLogExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	q := r.URL.Query()
	format := strings.ToLower(q.Get("format"))
	if format == "" {
		format = "json"
	}

	filter := storage.LogQueryFilter{
		Level:  q.Get("level"),
		Source: q.Get("source"),
		Search: q.Get("search"),
		Limit:  10000,
	}

	records, _, err := s.store.QueryLogs(filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	filename := fmt.Sprintf("velox-logs-%s.%s", time.Now().Format("20060102-150405"), format)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))

	if format == "ndjson" {
		w.Header().Set("Content-Type", "application/x-ndjson")
		enc := json.NewEncoder(w)
		for _, rec := range records {
			_ = enc.Encode(rec)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(records)
}

// API: Get or Save Benchmark Methodology Chain
func (s *Server) handleBenchmarkMethodology(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		chain, err := s.store.GetBenchmarkMethodology()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"chain":   chain,
		})
	case http.MethodPost:
		var chain model.BenchmarkMethodologyChain
		if err := json.NewDecoder(r.Body).Decode(&chain); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
		if err := chain.Validate(); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.store.SaveBenchmarkMethodology(&chain); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.jobMgr.Log("info", "methodology", fmt.Sprintf("Updated benchmark methodology: %d steps, scoring_mode=%s", len(chain.Steps), chain.ScoringMode))
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"chain":   chain,
		})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}


// localOnly rejects requests that did not come from the dashboard itself.
// The API has no login (admins reach it through an SSH tunnel), so this blocks
// other web pages in the admin's browser (CSRF) and DNS-rebinding hosts.
func localOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if host != "127.0.0.1" && host != "localhost" && host != "[::1]" && host != "::1" {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if o := r.Header.Get("Origin"); o != "" && o != "http://"+r.Host {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
