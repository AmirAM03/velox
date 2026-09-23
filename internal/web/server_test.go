package web

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/AmirAM03/velox/internal/config"
	loggerPkg "github.com/AmirAM03/velox/internal/logger"
	"github.com/AmirAM03/velox/internal/storage"
)

func setupTestServer(t *testing.T) (*Server, *storage.Store, *loggerPkg.DBHandler) {
	dbPath := filepath.Join(t.TempDir(), "web_test.db")
	store, err := storage.Open(dbPath, nil)
	if err != nil {
		t.Fatalf("Open DB: %v", err)
	}

	cfg := config.DefaultConfig()
	dbHandler := loggerPkg.NewDBHandler(loggerPkg.Config{
		Store: store,
		Level: slog.LevelDebug,
	})

	srv := NewServer(cfg, store, 18080, slog.New(dbHandler), dbHandler)
	return srv, store, dbHandler
}

func TestServer_LogsEndpoints(t *testing.T) {
	srv, store, dbHandler := setupTestServer(t)
	defer store.Close()
	defer dbHandler.Close()

	// 1. Insert initial logs
	logs := []*storage.LogRecord{
		{
			Timestamp: time.Now().Add(-10 * time.Minute),
			Level:     "INFO",
			Source:    "pipeline",
			Message:   "Starting benchmark pipeline",
			Attrs:     map[string]any{"threads": 50},
		},
		{
			Timestamp: time.Now().Add(-5 * time.Minute),
			Level:     "ERROR",
			Source:    "engine",
			Message:   "Connection to 1.2.3.4 timed out",
			Attrs:     map[string]any{"target": "google.com", "code": 504},
		},
	}
	if err := store.InsertLogsBatch(logs); err != nil {
		t.Fatalf("InsertLogsBatch: %v", err)
	}

	// 2. Test GET /api/logs
	req := httptest.NewRequest(http.MethodGet, "/api/logs?level=ERROR", nil)
	w := httptest.NewRecorder()
	srv.handleLogs(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("handleLogs status = %d, expected 200", w.Code)
	}

	var logsResp struct {
		Logs  []*storage.LogRecord `json:"logs"`
		Total int64                `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &logsResp); err != nil {
		t.Fatalf("Unmarshal logsResp: %v", err)
	}
	if logsResp.Total != 1 || len(logsResp.Logs) != 1 || logsResp.Logs[0].Level != "ERROR" {
		t.Errorf("expected 1 ERROR log, got %+v", logsResp)
	}

	// 3. Test GET /api/logs/stats
	req = httptest.NewRequest(http.MethodGet, "/api/logs/stats", nil)
	w = httptest.NewRecorder()
	srv.handleLogStats(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("handleLogStats status = %d", w.Code)
	}
	var stats storage.LogStats
	if err := json.Unmarshal(w.Body.Bytes(), &stats); err != nil {
		t.Fatalf("Unmarshal stats: %v", err)
	}
	if stats.TotalCount != 2 || stats.LevelCounts["ERROR"] != 1 {
		t.Errorf("unexpected stats: %+v", stats)
	}

	// 4. Test POST /api/logs/retention
	retBody, _ := json.Marshal(map[string]string{"retention": "30d"})
	req = httptest.NewRequest(http.MethodPost, "/api/logs/retention", bytes.NewReader(retBody))
	w = httptest.NewRecorder()
	srv.handleLogRetention(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("handleLogRetention status = %d", w.Code)
	}
	setting, _ := store.GetSetting("log_retention", "")
	if setting != "30d" {
		t.Errorf("expected retention 30d, got %q", setting)
	}

	// 5. Test GET /api/logs/export
	req = httptest.NewRequest(http.MethodGet, "/api/logs/export?format=json", nil)
	w = httptest.NewRecorder()
	srv.handleLogExport(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("handleLogExport status = %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}

	// 6. Test POST /api/logs/clear
	req = httptest.NewRequest(http.MethodPost, "/api/logs/clear", nil)
	w = httptest.NewRecorder()
	srv.handleLogClear(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("handleLogClear status = %d", w.Code)
	}
	clearedStats, _ := store.GetLogStats()
	if clearedStats.TotalCount != 0 {
		t.Errorf("expected 0 logs after clear, got %d", clearedStats.TotalCount)
	}
}

