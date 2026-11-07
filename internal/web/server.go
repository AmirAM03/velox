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
	"github.com/AmirAM03/velox/internal/storage"
)

// Server manages the web dashboard HTTP server and background proxy state.
type Server struct {
	cfg       *config.Config
	store     *storage.Store
	logger    *slog.Logger
	dbHandler *loggerPkg.DBHandler
	port      int
	hub       *Hub
	profiles  *ProfileManager
	optimizer *Optimizer
	logins    logins
}

// NewServer creates a new web dashboard server.
func NewServer(cfg *config.Config, store *storage.Store, port int, logger *slog.Logger, dbHandler *loggerPkg.DBHandler) *Server {
	if port <= 0 {
		port = 18080
	}
	if logger == nil {
		logger = slog.Default()
	}

	hub := NewHub()
	if dbHandler != nil {
		dbHandler.AddBroadcast(func(rec *storage.LogRecord) {
			hub.Broadcast(map[string]any{"type": "app_log", "log": rec})
		})
	}

	s := &Server{
		cfg:       cfg,
		store:     store,
		logger:    logger,
		dbHandler: dbHandler,
		port:      port,
		hub:       hub,
	}

	s.profiles = NewProfileManager(store, cfg.Proxy.ListenAddr, cfg.Proxy.FailThreshold, logger)
	s.optimizer = NewOptimizer(store, s.profiles, logger, hub.Broadcast)

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
		if r.URL.Path == "/" && r.URL.Query().Get("state") != "" {
			s.handleLoginCallback(w, r) // the OAuth provider redirects back to the dashboard root
			return
		}
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
	mux.Handle("/api/events", s.hub)

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
	mux.HandleFunc("/api/sources/paste", s.handleSourcePaste)
	mux.HandleFunc("/api/sources/delete", s.handleSourceDelete)
	mux.HandleFunc("/api/profiles", s.handleProfiles)
	mux.HandleFunc("/api/profiles/test", s.handleProfileTest)
	mux.HandleFunc("/api/profiles/results", s.handleProfileResults)
	mux.HandleFunc("/api/profiles/active", s.handleProfileActive)
	mux.HandleFunc("/api/profiles/login", s.handleProfileLogin)
	mux.HandleFunc("/api/profiles/logout", s.handleProfileLogout)
	mux.HandleFunc("/api/optimizer", s.handleOptimizer)
	mux.HandleFunc("/api/optimizer/run", s.handleOptimizerRun)

	// Start profile listeners and the background optimizer
	if err := s.seedProfiles(); err != nil {
		return err
	}
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
	filter := storage.ConfigQueryFilter{
		Protocol: protocol,
		Search:   search,
		SortBy:   q.Get("sort_by"),
		Limit:    limit,
		Offset:   offset,
	}

	items, total, err := s.store.QueryConfigs(filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"configs": items,
		"total":   total,
		"limit":   limit,
		"offset":  offset,
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
