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
	"github.com/AmirAM03/velox/internal/model"
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

func TestServer_ConfigsFilteringAndSorting(t *testing.T) {
	srv, store, dbHandler := setupTestServer(t)
	defer store.Close()
	defer dbHandler.Close()

	// Insert configs
	cfgs := []*model.ProxyConfig{
		{
			ID: "node-a", Name: "Alpha Node", Protocol: model.ProtocolVLESS, Address: "1.1.1.1", Port: 443, RawURI: "vless://a",
		},
		{
			ID: "node-b", Name: "Beta Node", Protocol: model.ProtocolVMess, Address: "2.2.2.2", Port: 443, RawURI: "vmess://b",
		},
		{
			ID: "node-c", Name: "Gamma Node", Protocol: model.ProtocolTrojan, Address: "3.3.3.3", Port: 443, RawURI: "trojan://c",
		},
	}
	for _, c := range cfgs {
		_ = store.UpsertConfig(c)
	}

	// Insert scores: node-a (working, lat 45), node-b (failed, composite 999999), node-c (working, lat 15)
	_ = store.UpsertScore(&model.Score{ConfigID: "node-a", Composite: 50, LatencyScore: 45, SuccessScore: 0.0})
	_ = store.UpsertScore(&model.Score{ConfigID: "node-b", Composite: 999999, LatencyScore: 0, SuccessScore: 1.0})
	_ = store.UpsertScore(&model.Score{ConfigID: "node-c", Composite: 20, LatencyScore: 15, SuccessScore: 0.0})

	// 1. Test working_only=true
	req := httptest.NewRequest(http.MethodGet, "/api/configs?working_only=true", nil)
	w := httptest.NewRecorder()
	srv.handleConfigs(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("handleConfigs status = %d", w.Code)
	}

	var res struct {
		Configs []storage.ConfigItem `json:"configs"`
		Total   int                  `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("Unmarshal configs response: %v", err)
	}
	if res.Total != 2 || len(res.Configs) != 2 {
		t.Fatalf("expected 2 working configs, got total=%d, len=%d", res.Total, len(res.Configs))
	}

	// 2. Test working_only=true & sort_by=latency (Gamma 15ms should be first)
	req = httptest.NewRequest(http.MethodGet, "/api/configs?working_only=true&sort_by=latency", nil)
	w = httptest.NewRecorder()
	srv.handleConfigs(w, req)
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("Unmarshal configs response: %v", err)
	}
	if len(res.Configs) < 2 || res.Configs[0].ID != "node-c" || res.Configs[1].ID != "node-a" {
		t.Errorf("expected node-c first (15ms), got %+v", res.Configs)
	}

	// 3. Test protocol filter
	req = httptest.NewRequest(http.MethodGet, "/api/configs?protocol=trojan", nil)
	w = httptest.NewRecorder()
	srv.handleConfigs(w, req)
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("Unmarshal configs response: %v", err)
	}
	if res.Total != 1 || res.Configs[0].ID != "node-c" {
		t.Errorf("expected 1 trojan config, got %+v", res)
	}
}

func TestServer_RotationEndpoints(t *testing.T) {
	srv, store, dbHandler := setupTestServer(t)
	defer store.Close()
	defer dbHandler.Close()

	// Seed configs and scores
	_ = store.UpsertConfig(&model.ProxyConfig{ID: "node-1", Name: "N1", Protocol: model.ProtocolVLESS, Address: "1.1.1.1", Port: 443})
	_ = store.UpsertConfig(&model.ProxyConfig{ID: "node-2", Name: "N2", Protocol: model.ProtocolVMess, Address: "2.2.2.2", Port: 443})
	_ = store.UpsertScore(&model.Score{ConfigID: "node-1", Composite: 25, LatencyScore: 25, SuccessScore: 0.0})

	// 1. Add node-1 and node-2 to rotation pool
	addBody, _ := json.Marshal(map[string]any{
		"config_ids": []string{"node-1", "node-2"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/rotation/pool/add", bytes.NewReader(addBody))
	w := httptest.NewRecorder()
	srv.handleRotationPoolAdd(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("handleRotationPoolAdd status = %d", w.Code)
	}

	// 2. Query pool
	req = httptest.NewRequest(http.MethodGet, "/api/rotation/pool", nil)
	w = httptest.NewRecorder()
	srv.handleRotationPool(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("handleRotationPool status = %d", w.Code)
	}
	var poolResp struct {
		Total   int64 `json:"total"`
		Working int64 `json:"working"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &poolResp)
	if poolResp.Total != 2 || poolResp.Working != 1 {
		t.Errorf("expected pool total=2, working=1, got %+v", poolResp)
	}

	// 3. Configure rotation
	cfgBody, _ := json.Marshal(map[string]any{
		"enabled":  true,
		"interval": "5m",
		"target":   "https://www.google.com/generate_204",
		"threads":  20,
	})
	req = httptest.NewRequest(http.MethodPost, "/api/rotation/config", bytes.NewReader(cfgBody))
	w = httptest.NewRecorder()
	srv.handleRotationConfig(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("handleRotationConfig status = %d", w.Code)
	}
	var rotStatus RotationStatus
	_ = json.Unmarshal(w.Body.Bytes(), &rotStatus)
	if !rotStatus.Enabled || rotStatus.Interval != "5m" {
		t.Errorf("expected rotation enabled with 5m interval, got %+v", rotStatus)
	}

	// 4. Remove node-2
	remBody, _ := json.Marshal(map[string]any{
		"config_id": "node-2",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/rotation/pool/remove", bytes.NewReader(remBody))
	w = httptest.NewRecorder()
	srv.handleRotationPoolRemove(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("handleRotationPoolRemove status = %d", w.Code)
	}

	// 5. Clear pool
	req = httptest.NewRequest(http.MethodPost, "/api/rotation/pool/clear", nil)
	w = httptest.NewRecorder()
	srv.handleRotationPoolClear(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("handleRotationPoolClear status = %d", w.Code)
	}
	tot, _, _ := store.GetRotationPoolStats()
	if tot != 0 {
		t.Errorf("expected 0 configs in pool after clear, got %d", tot)
	}

	// 6. Add working to pool
	req = httptest.NewRequest(http.MethodPost, "/api/rotation/pool/add-working", nil)
	w = httptest.NewRecorder()
	srv.handleRotationPoolAddWorking(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("handleRotationPoolAddWorking status = %d", w.Code)
	}
	tot, _, _ = store.GetRotationPoolStats()
	if tot != 1 {
		t.Errorf("expected 1 working config added to pool, got %d", tot)
	}
}

func TestServer_BenchmarkMethodologyEndpoints(t *testing.T) {
	srv, store, dbHandler := setupTestServer(t)
	defer store.Close()
	defer dbHandler.Close()

	// 1. Initial GET should return default chain
	req := httptest.NewRequest(http.MethodGet, "/api/benchmark/methodology", nil)
	w := httptest.NewRecorder()
	srv.handleBenchmarkMethodology(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/benchmark/methodology status = %d", w.Code)
	}

	var getResp struct {
		Success bool                             `json:"success"`
		Chain   model.BenchmarkMethodologyChain `json:"chain"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("unmarshal GET response: %v", err)
	}
	if !getResp.Success || len(getResp.Chain.Steps) == 0 {
		t.Errorf("expected successful GET with default steps, got %+v", getResp)
	}

	// 2. POST updated chain
	newChain := model.BenchmarkMethodologyChain{
		ScoringMode: model.ScoringModeWeighted,
		Steps: []model.TestStepConfig{
			{
				ID:        "step-url-only",
				Type:      model.MethodologyHTTPDelay,
				Name:      "Direct Google Ping",
				Enabled:   true,
				Necessary: true,
				Priority:  1,
				Weight:    1.0,
				IsPrimary: true,
				TimeoutMS: 3000,
				TargetURL: "https://www.google.com/generate_204",
			},
		},
	}
	body, _ := json.Marshal(newChain)
	req = httptest.NewRequest(http.MethodPost, "/api/benchmark/methodology", bytes.NewReader(body))
	w = httptest.NewRecorder()
	srv.handleBenchmarkMethodology(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/benchmark/methodology status = %d, body = %s", w.Code, w.Body.String())
	}

	// 3. Verify changes persisted
	req = httptest.NewRequest(http.MethodGet, "/api/benchmark/methodology", nil)
	w = httptest.NewRecorder()
	srv.handleBenchmarkMethodology(w, req)
	_ = json.Unmarshal(w.Body.Bytes(), &getResp)
	if getResp.Chain.ScoringMode != model.ScoringModeWeighted {
		t.Errorf("expected ScoringMode %s, got %s", model.ScoringModeWeighted, getResp.Chain.ScoringMode)
	}
	if len(getResp.Chain.Steps) != 1 || getResp.Chain.Steps[0].ID != "step-url-only" {
		t.Errorf("expected 1 step with id step-url-only, got %+v", getResp.Chain.Steps)
	}
}


