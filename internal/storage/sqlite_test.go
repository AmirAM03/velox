package storage

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/AmirAM03/velox/internal/model"
)

func TestStorage_Lifecycle(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := Open(dbPath, nil)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	// 1. Initial count should be 0
	count, err := store.ConfigCount()
	if err != nil {
		t.Fatalf("ConfigCount: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 configs, got %d", count)
	}

	// 2. Insert single config
	cfg1 := &model.ProxyConfig{
		ID:        "cfg-test-1",
		Name:      "Test VLESS",
		RawURI:    "vless://user@1.1.1.1:443?security=reality",
		Protocol:  model.ProtocolVLESS,
		Source:    "manual",
		Address:   "1.1.1.1",
		Port:      443,
		UUID:      "test-uuid-1",
		Security:  model.SecurityREALITY,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := store.UpsertConfig(cfg1); err != nil {
		t.Fatalf("UpsertConfig: %v", err)
	}

	count, _ = store.ConfigCount()
	if count != 1 {
		t.Errorf("expected 1 config, got %d", count)
	}

	// 3. Batch upsert
	cfg2 := &model.ProxyConfig{
		ID:        "cfg-test-2",
		Name:      "Test VMess",
		RawURI:    "vmess://...",
		Protocol:  model.ProtocolVMess,
		Source:    "subscription",
		Address:   "2.2.2.2",
		Port:      8443,
		UUID:      "test-uuid-2",
		Security:  model.SecurityTLS,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	inserted, updated, err := store.UpsertConfigs([]*model.ProxyConfig{cfg1, cfg2})
	if err != nil {
		t.Fatalf("UpsertConfigs: %v", err)
	}
	if count, _ := store.ConfigCount(); count != 2 {
		t.Errorf("expected 2 configs after batch, got %d (inserted=%d, updated=%d)", count, inserted, updated)
	}

	// 4. Protocol counts
	pCounts, err := store.ConfigCountByProtocol()
	if err != nil {
		t.Fatalf("ConfigCountByProtocol: %v", err)
	}
	if pCounts[string(model.ProtocolVLESS)] != 1 || pCounts[string(model.ProtocolVMess)] != 1 {
		t.Errorf("unexpected protocol counts: %+v", pCounts)
	}

	// 5. Test results insertion
	results := []model.TestResult{
		{
			ConfigID:  cfg1.ID,
			Stage:     model.StageDNSTCP,
			Success:   true,
			Latency:   30 * time.Millisecond,
			TestedAt:  time.Now(),
		},
		{
			ConfigID:  cfg1.ID,
			Stage:     model.StageTLS,
			Success:   true,
			Latency:   80 * time.Millisecond,
			TestedAt:  time.Now(),
		},
	}
	if err := store.InsertTestResults(results); err != nil {
		t.Fatalf("InsertTestResults: %v", err)
	}

	// 6. Score upsert and retrieval
	score := &model.Score{
		ConfigID:     cfg1.ID,
		Composite:    0.15,
		LatencyScore: 0.1,
		SuccessScore: 0.0,
		TestCount:    2,
		LastTestedAt: time.Now(),
		UpdatedAt:    time.Now(),
	}
	if err := store.UpsertScore(score); err != nil {
		t.Fatalf("UpsertScore: %v", err)
	}

	retrievedScore, err := store.GetScore(cfg1.ID)
	if err != nil {
		t.Fatalf("GetScore: %v", err)
	}
	if retrievedScore == nil || retrievedScore.Composite != 0.15 {
		t.Errorf("unexpected retrieved score: %+v", retrievedScore)
	}

	// 7. ListConfigs ordering
	list, err := store.ListConfigs(10)
	if err != nil {
		t.Fatalf("ListConfigs: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 configs in list, got %d", len(list))
	}
	// cfg1 has score 0.15, cfg2 has no score (defaults to 999999), so cfg1 should be first
	if list[0].ID != cfg1.ID {
		t.Errorf("expected cfg1 to be first, got %s", list[0].ID)
	}

	// 8. DeleteConfig
	if err := store.DeleteConfig(cfg1.ID); err != nil {
		t.Fatalf("DeleteConfig: %v", err)
	}
	count, _ = store.ConfigCount()
	if count != 1 {
		t.Errorf("expected 1 config after deletion, got %d", count)
	}
}

func TestStorage_LogsAndSettings(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "logs_test.db")
	store, err := Open(dbPath, nil)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	// Settings
	if err := store.SetSetting("log_retention", "30d"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	val, err := store.GetSetting("log_retention", "7d")
	if err != nil || val != "30d" {
		t.Fatalf("expected 30d, got %v (err: %v)", val, err)
	}

	// Insert log batch
	tOld := time.Now().Add(-48 * time.Hour)
	tNow := time.Now()
	logs := []*LogRecord{
		{
			Timestamp: tOld,
			Level:     "INFO",
			Source:    "pipeline",
			Message:   "Ingestion started",
			Attrs:     map[string]any{"source_url": "https://example.com"},
		},
		{
			Timestamp: tNow,
			Level:     "ERROR",
			Source:    "engine",
			Message:   "Connection handshake failed",
			Attrs:     map[string]any{"target": "google.com", "code": 504},
		},
		{
			Timestamp: tNow,
			Level:     "WARN",
			Source:    "proxy",
			Message:   "High latency detected",
			Attrs:     map[string]any{"latency_ms": 1200},
		},
	}

	if err := store.InsertLogsBatch(logs); err != nil {
		t.Fatalf("InsertLogsBatch: %v", err)
	}

	// Query all
	records, total, err := store.QueryLogs(LogQueryFilter{})
	if err != nil {
		t.Fatalf("QueryLogs: %v", err)
	}
	if total != 3 || len(records) != 3 {
		t.Fatalf("expected 3 logs, got %d total and %d records", total, len(records))
	}

	// Query by level
	errLogs, totalErr, err := store.QueryLogs(LogQueryFilter{Level: "error"})
	if err != nil {
		t.Fatalf("QueryLogs level: %v", err)
	}
	if totalErr != 1 || len(errLogs) != 1 || errLogs[0].Level != "ERROR" {
		t.Fatalf("expected 1 error log, got %d", totalErr)
	}

	// Query by search
	searchLogs, totalSearch, err := store.QueryLogs(LogQueryFilter{Search: "handshake"})
	if err != nil {
		t.Fatalf("QueryLogs search: %v", err)
	}
	if totalSearch != 1 || len(searchLogs) != 1 {
		t.Fatalf("expected 1 search match, got %d", totalSearch)
	}

	// Stats
	stats, err := store.GetLogStats()
	if err != nil {
		t.Fatalf("GetLogStats: %v", err)
	}
	if stats.TotalCount != 3 || stats.LevelCounts["ERROR"] != 1 || stats.Retention != "30d" {
		t.Fatalf("unexpected stats: %+v", stats)
	}

	// Prune logs older than 24 hours (should remove 1)
	pruned, err := store.PruneLogs(24 * time.Hour)
	if err != nil {
		t.Fatalf("PruneLogs: %v", err)
	}
	if pruned != 1 {
		t.Fatalf("expected 1 pruned log, got %d", pruned)
	}
	recordsAfterPrune, _, _ := store.QueryLogs(LogQueryFilter{})
	if len(recordsAfterPrune) != 2 {
		t.Fatalf("expected 2 logs after prune, got %d", len(recordsAfterPrune))
	}

	// Clear logs
	if err := store.ClearLogs(); err != nil {
		t.Fatalf("ClearLogs: %v", err)
	}
	emptyRecords, totalEmpty, _ := store.QueryLogs(LogQueryFilter{})
	if totalEmpty != 0 || len(emptyRecords) != 0 {
		t.Fatalf("expected 0 logs after clear, got %d", totalEmpty)
	}
}

