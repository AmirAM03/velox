package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/AmirAM03/velox/internal/config"
	loggerPkg "github.com/AmirAM03/velox/internal/logger"
	"github.com/AmirAM03/velox/internal/model"
	"github.com/AmirAM03/velox/internal/pipeline"
	"github.com/AmirAM03/velox/internal/storage"
)

func setupTestServer(t *testing.T) (*Server, *storage.Store, *loggerPkg.DBHandler) {
	dbPath := filepath.Join(t.TempDir(), "web_test.db")
	store, err := storage.Open(dbPath, nil)
	if err != nil {
		t.Fatalf("Open DB: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Proxy.ListenAddr = "127.0.0.1" // keep test listeners off public interfaces
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



func TestJobManager_CancelDoesNotDeadlock(t *testing.T) {
	srv, store, dbHandler := setupTestServer(t)
	defer store.Close()
	defer dbHandler.Close()

	// 10.255.255.1 is unroutable, so the TCP step hangs until its timeout and the job is still running when we cancel.
	cfgs := []*model.ProxyConfig{{ID: "slow", Protocol: model.ProtocolVLESS, Address: "10.255.255.1", Port: 443}}
	if err := srv.jobMgr.Start("https://example.com", 1, cfgs); err != nil {
		t.Fatalf("Start: %v", err)
	}

	done := make(chan bool)
	go func() { done <- srv.jobMgr.Cancel() }()
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("Cancel returned false for a running job")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Cancel deadlocked")
	}

	// A new run must be refused until the cancelled one has really stopped.
	if err := srv.jobMgr.Start("https://example.com", 1, cfgs); err == nil {
		t.Error("Start succeeded while the cancelled run was still winding down")
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		srv.jobMgr.mu.RLock()
		st := srv.jobMgr.status
		srv.jobMgr.mu.RUnlock()
		if st == StatusCancelled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("status = %s, want cancelled", st)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestLocalOnly(t *testing.T) {
	h := localOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	cases := []struct {
		host, origin string
		want         int
	}{
		{"127.0.0.1:18080", "", http.StatusOK},
		{"localhost:9000", "http://localhost:9000", http.StatusOK}, // SSH tunnel on another local port
		{"127.0.0.1:18080", "https://evil.example", http.StatusForbidden},
		{"evil.example:18080", "", http.StatusForbidden}, // DNS rebinding
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodPost, "/api/connect", nil)
		req.Host = c.host
		if c.origin != "" {
			req.Header.Set("Origin", c.origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != c.want {
			t.Errorf("host=%s origin=%s: got %d, want %d", c.host, c.origin, w.Code, c.want)
		}
	}
}

func TestServer_ProfileAndSourceAPI(t *testing.T) {
	srv, store, dbHandler := setupTestServer(t)
	defer store.Close()
	defer dbHandler.Close()
	defer srv.Close()

	post := func(h http.HandlerFunc, body any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		w := httptest.NewRecorder()
		h(w, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(b)))
		return w
	}

	// Sources: only http(s) URLs, no duplicates.
	if w := post(srv.handleSources, map[string]string{"url": "ftp://x"}); w.Code != http.StatusBadRequest {
		t.Errorf("ftp source: got %d, want 400", w.Code)
	}
	if w := post(srv.handleSources, map[string]string{"url": "https://example.com/sub"}); w.Code != http.StatusOK {
		t.Fatalf("add source: %d %s", w.Code, w.Body)
	}
	if w := post(srv.handleSources, map[string]string{"url": "https://example.com/sub"}); w.Code != http.StatusConflict {
		t.Errorf("duplicate source: got %d, want 409", w.Code)
	}

	// Profiles: credentials are mandatory because the port is public.
	noAuth := storage.Profile{Name: "yt", TargetURL: "youtube.com", Port: 0, Enabled: true}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	noAuth.Port = ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	if w := post(srv.handleProfiles, noAuth); w.Code != http.StatusBadRequest {
		t.Errorf("profile without credentials: got %d, want 400", w.Code)
	}
	good := noAuth
	good.Username, good.Password = "u", "p"
	if w := post(srv.handleProfiles, good); w.Code != http.StatusOK {
		t.Fatalf("create profile: %d %s", w.Code, w.Body)
	}
	st, _ := srv.profiles.Status()
	if len(st) != 1 || !st[0].Running || st[0].TargetURL != "https://youtube.com" {
		t.Fatalf("profile status = %+v, want one running profile with normalized target", st)
	}
	// The listener must demand auth.
	c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", good.Port))
	if err != nil {
		t.Fatalf("profile port not listening: %v", err)
	}
	c.Write([]byte{0x05, 0x01, 0x00})
	reply := make([]byte, 2)
	io.ReadFull(c, reply)
	c.Close()
	if reply[1] != 0xFF {
		t.Errorf("no-auth SOCKS5 to profile port: got method %#x, want 0xFF", reply[1])
	}

	if w := post(srv.handleProfileDelete, map[string]int64{"id": st[0].ID}); w.Code != http.StatusOK {
		t.Fatalf("delete profile: %d", w.Code)
	}
	if _, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", good.Port), time.Second); err == nil {
		t.Error("listener still open after profile delete")
	}
}

func TestOptimizer_SplitAndRank(t *testing.T) {
	base, httpStep := splitChain(model.DefaultMethodologyChain())
	if len(base) != 2 || httpStep.Type != model.MethodologyHTTPDelay {
		t.Fatalf("split: base=%d http=%s", len(base), httpStep.Type)
	}
	for _, s := range base {
		if !s.Necessary {
			t.Errorf("base step %s must be a hard filter", s.ID)
		}
	}

	mk := func(id string, ok bool, ms int) pipeline.Result {
		return pipeline.Result{
			Config:  &model.ProxyConfig{ID: id},
			Results: []model.TestResult{{Success: ok, Latency: time.Duration(ms) * time.Millisecond}},
		}
	}
	nodes, cfgs := rankProfileNodes([]pipeline.Result{mk("slow", true, 300), mk("dead", false, 10), mk("fast", true, 50)})
	if len(nodes) != 2 || cfgs[0].ID != "fast" || nodes[0].Rank != 1 || nodes[1].ConfigID != "slow" {
		t.Errorf("rank = %+v", nodes)
	}
}
