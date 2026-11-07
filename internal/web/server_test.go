package web

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AmirAM03/velox/internal/bench"
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

	var res struct {
		Configs []storage.ConfigItem `json:"configs"`
		Total   int                  `json:"total"`
	}
	req := httptest.NewRequest(http.MethodGet, "/api/configs?sort_by=name&search=a+Node", nil)
	w := httptest.NewRecorder()
	srv.handleConfigs(w, req)
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || w.Code != http.StatusOK {
		t.Fatalf("handleConfigs = %d %s", w.Code, w.Body)
	}
	if res.Total != 3 || res.Configs[0].ID != "node-a" || res.Configs[2].ID != "node-c" {
		t.Errorf("name sort = %+v, want Alpha, Beta, Gamma", res.Configs)
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

	// Paste: plain lists and base64 bodies import; duplicates collapse; garbage is rejected.
	ss := "ss://YWVzLTEyOC1nY206cHc@1.2.3.4:8388#a"
	if w := post(srv.handleSourcePaste, map[string]string{"content": ss + "\n" + ss + "#dup\nnot-a-config"}); w.Code != http.StatusOK ||
		!strings.Contains(w.Body.String(), `"configs":1`) || !strings.Contains(w.Body.String(), `"new":1`) {
		t.Errorf("paste list: %d %s, want 1 config, 1 new", w.Code, w.Body)
	}
	b64 := base64.StdEncoding.EncodeToString([]byte(ss + "\ntrojan://pw@5.6.7.8:443#b"))
	if w := post(srv.handleSourcePaste, map[string]string{"content": b64}); w.Code != http.StatusOK ||
		!strings.Contains(w.Body.String(), `"configs":2`) || !strings.Contains(w.Body.String(), `"new":1`) {
		t.Errorf("paste base64: %d %s, want 2 configs, 1 new", w.Code, w.Body)
	}
	if w := post(srv.handleSourcePaste, map[string]string{"content": "hello"}); w.Code != http.StatusBadRequest {
		t.Errorf("paste garbage: got %d, want 400", w.Code)
	}

	// Profiles: the built-in types exist from the start, disabled; they can only be edited.
	if err := srv.seedProfiles(); err != nil {
		t.Fatal(err)
	}
	st, _ := srv.profiles.Status()
	if len(st) != 1 || st[0].Kind != "antigravity" || st[0].Enabled || len(st[0].Type.Fields) == 0 {
		t.Fatalf("seeded profiles = %+v, want one disabled Antigravity profile", st)
	}
	id := st[0].ID
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	edit := map[string]any{"id": id, "port": port, "enabled": true, "auto_test": false}
	if w := post(srv.handleProfiles, edit); w.Code != http.StatusBadRequest {
		t.Errorf("profile without credentials: got %d, want 400", w.Code)
	}
	edit["username"], edit["password"] = "u", "p"
	if w := post(srv.handleProfiles, edit); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "settings") {
		t.Errorf("enabled profile without session: got %d %s, want 400 about settings", w.Code, w.Body)
	}
	edit["settings"] = map[string]string{"refresh_token": `{"token":{"refresh_token":"1//secret-rt"}}`, "client_secret": "GOCSPX-secret"}
	if w := post(srv.handleProfiles, edit); w.Code != http.StatusOK {
		t.Fatalf("save profile: %d %s", w.Code, w.Body)
	}
	// Secrets never go back to the dashboard, and saving with them empty keeps them.
	edit["settings"] = map[string]string{}
	if w := post(srv.handleProfiles, edit); w.Code != http.StatusOK {
		t.Fatalf("save profile again: %d %s", w.Code, w.Body)
	}
	w := httptest.NewRecorder()
	srv.handleProfiles(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if strings.Contains(w.Body.String(), "1//secret-rt") || strings.Contains(w.Body.String(), "GOCSPX-secret") || !strings.Contains(w.Body.String(), `"secrets_set":["client_secret","refresh_token"]`) {
		t.Errorf("profiles API leaks or lost secrets: %s", w.Body)
	}
	if ps, _ := store.ListProfiles(); ps[0].Settings["client_secret"] != "GOCSPX-secret" {
		t.Errorf("stored settings = %v, want secrets kept", ps[0].Settings)
	}
	if w := post(srv.handleProfiles, map[string]any{"id": 999, "port": port + 1, "username": "u", "password": "p"}); w.Code != http.StatusNotFound {
		t.Errorf("unknown profile: got %d, want 404 (profiles cannot be added)", w.Code)
	}

	// The listener must demand auth.
	c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
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

	edit["enabled"] = false
	if w := post(srv.handleProfiles, edit); w.Code != http.StatusOK {
		t.Fatalf("disable profile: %d", w.Code)
	}
	if _, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second); err == nil {
		t.Error("listener still open after the profile was disabled")
	}
}

// While a profile is being checked, candidates found so far lead (best first)
// and the previous nodes follow as fallbacks, without duplicates, capped.
func TestLivePool(t *testing.T) {
	c := func(id string) *model.ProxyConfig { return &model.ProxyConfig{ID: id} }
	done := []bench.Result{{Config: c("new-best")}, {Config: c("old-2")}}
	pool := livePool(done, []*model.ProxyConfig{c("old-1"), c("old-2"), c("old-3")})
	var ids []string
	for _, p := range pool {
		ids = append(ids, p.ID)
	}
	if got := strings.Join(ids, ","); got != "new-best,old-2,old-1,old-3" {
		t.Errorf("live pool = %s", got)
	}

	var many []*model.ProxyConfig
	for i := 0; i < 2*nodesPerProfile; i++ {
		many = append(many, c(fmt.Sprint(i)))
	}
	if n := len(livePool(nil, many)); n != nodesPerProfile {
		t.Errorf("pool size %d, want cap %d", n, nodesPerProfile)
	}
}
