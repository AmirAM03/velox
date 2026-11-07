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

	// 6. Listing
	list, err := store.ListConfigsFiltered("", 0)
	if err != nil || len(list) != 2 {
		t.Fatalf("ListConfigsFiltered = %d configs, %v; want 2", len(list), err)
	}
	if vless, _ := store.ListConfigsFiltered(string(model.ProtocolVLESS), 0); len(vless) != 1 || vless[0].ID != cfg1.ID {
		t.Errorf("protocol filter returned %v, want only cfg1", vless)
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

func TestStorage_QueryConfigs(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pool_test.db")
	store, err := Open(dbPath, nil)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	// Insert 3 configs
	cfg1 := &model.ProxyConfig{
		ID:        "cfg-1",
		Name:      "Alpha VLESS Fast",
		RawURI:    "vless://user@1.1.1.1:443",
		Protocol:  model.ProtocolVLESS,
		Address:   "1.1.1.1",
		Port:      443,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	cfg2 := &model.ProxyConfig{
		ID:        "cfg-2",
		Name:      "Beta Trojan Slow",
		RawURI:    "trojan://pass@2.2.2.2:443",
		Protocol:  model.ProtocolTrojan,
		Address:   "2.2.2.2",
		Port:      443,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	cfg3 := &model.ProxyConfig{
		ID:        "cfg-3",
		Name:      "Gamma VMess Untested",
		RawURI:    "vmess://...",
		Protocol:  model.ProtocolVMess,
		Address:   "3.3.3.3",
		Port:      8443,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if _, _, err := store.UpsertConfigs([]*model.ProxyConfig{cfg1, cfg2, cfg3}); err != nil {
		t.Fatalf("UpsertConfigs: %v", err)
	}

	// Default order: most recently listed by a source first.
	old := time.Now().Add(-time.Hour)
	cfg3.UpdatedAt = old
	if _, _, err := store.UpsertConfigs([]*model.ProxyConfig{cfg3}); err != nil {
		t.Fatal(err)
	}
	items, total, err := store.QueryConfigs(ConfigQueryFilter{})
	if err != nil || total != 3 || len(items) != 3 || items[2].ID != "cfg-3" {
		t.Fatalf("default order: total=%d items=%v err=%v, want cfg-3 last", total, items, err)
	}

	// Protocol filter, search, sort by name, paging.
	if items, total, _ := store.QueryConfigs(ConfigQueryFilter{Protocol: "trojan"}); total != 1 || items[0].ID != "cfg-2" {
		t.Errorf("protocol filter: total=%d", total)
	}
	if items, total, _ := store.QueryConfigs(ConfigQueryFilter{Search: "8443"}); total != 1 || items[0].ID != "cfg-3" {
		t.Errorf("search by port: total=%d", total)
	}
	if items, _, _ := store.QueryConfigs(ConfigQueryFilter{SortBy: "name", Limit: 1, Offset: 1}); len(items) != 1 || items[0].ID != "cfg-2" {
		t.Errorf("name sort page 2 = %v, want Beta (cfg-2)", items)
	}
	// Sort input never reaches SQL as-is.
	if _, _, err := store.QueryConfigs(ConfigQueryFilter{SortBy: "name; DROP TABLE configs"}); err != nil {
		t.Errorf("unknown sort should fall back to default, got %v", err)
	}
}

func TestStore_ProfilesNodesAndPrune(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "prof.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	old := time.Now().Add(-10 * 24 * time.Hour)
	stale := &model.ProxyConfig{ID: "stale", Protocol: model.ProtocolVLESS, Address: "1.1.1.1", Port: 1, CreatedAt: old, UpdatedAt: old}
	used := &model.ProxyConfig{ID: "used", Protocol: model.ProtocolVLESS, Address: "2.2.2.2", Port: 1, CreatedAt: old, UpdatedAt: old}
	fresh := &model.ProxyConfig{ID: "fresh", Protocol: model.ProtocolVLESS, Address: "3.3.3.3", Port: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if _, _, err := store.UpsertConfigs([]*model.ProxyConfig{stale, used, fresh}); err != nil {
		t.Fatal(err)
	}

	id, err := store.SaveProfile(Profile{Kind: "a", Name: "p", Port: 2000, Username: "u", Password: "p", Enabled: true, Settings: map[string]string{"k": "v"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveProfile(Profile{Kind: "b", Name: "dup", Port: 2000, Username: "u", Password: "p"}); err == nil {
		t.Error("expected unique-port error")
	}
	if _, err := store.SaveProfile(Profile{Kind: "a", Name: "dup", Port: 2001, Username: "u", Password: "p"}); err == nil {
		t.Error("expected one profile per kind")
	}
	if ps, _ := store.ListProfiles(); len(ps) != 1 || ps[0].Settings["k"] != "v" {
		t.Errorf("profiles = %+v, want settings kept", ps)
	}
	nodes := []ProfileNode{{ConfigID: "fresh", Rank: 2, OK: true, LatencyMS: 90, TestedAt: time.Now()}, {ConfigID: "used", Rank: 1, OK: true, LatencyMS: 40, Label: "DE", Response: `{"ok":true}`, TestedAt: time.Now()},
		{ConfigID: "stale", Label: "HK", Error: "not eligible", TestedAt: time.Now()}}
	if err := store.ReplaceProfileNodes(id, nodes); err != nil {
		t.Fatal(err)
	}
	if all, cfgs, _ := store.ListProfileNodes(id, false); len(all) != 3 || cfgs[2].ID != "stale" || all[2].OK || all[2].Label != "HK" {
		t.Fatalf("all results = %+v, want candidates then the failed one, labeled", all)
	}
	got, cfgs, err := store.ListProfileNodes(id, true)
	if err != nil || len(got) != 2 || cfgs[0].ID != "used" || cfgs[1].ID != "fresh" || got[0].Label != "DE" || got[0].Response != `{"ok":true}` {
		t.Fatalf("ListProfileNodes = %+v %v %v, want rank order used, fresh with response", got, cfgs, err)
	}

	// Stale configs go, unless they are a profile's candidates; a failed result does not keep one.
	pruned, err := store.PruneStaleConfigs(7 * 24 * time.Hour)
	if err != nil || pruned != 1 {
		t.Fatalf("pruned=%d err=%v, want 1", pruned, err)
	}
	if left, _ := store.GetConfigsByIDs([]string{"stale", "used", "fresh"}); len(left) != 2 {
		t.Errorf("want used+fresh left, got %d configs", len(left))
	}
}

// Free-form target-URL profiles from older versions: an Antigravity one becomes the
// built-in Antigravity profile (same port and credentials); the others are removed.
func TestStore_MigratesLegacyProfiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	store, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`DROP INDEX idx_profiles_kind`, // an old database has neither the index nor kinds
		`ALTER TABLE profiles ADD COLUMN target_url TEXT NOT NULL DEFAULT ''`,
		`INSERT INTO profiles (name, target_url, port, username, password) VALUES ('yt', 'https://youtube.com', 3000, 'u', 'p')`,
		`INSERT INTO profiles (name, target_url, port, username, password) VALUES ('AG', 'https://antigravity.google.com/remote', 3001, 'tobi', 'pw')`,
	} {
		if _, err := store.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	store.Close()

	store, err = Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ps, err := store.ListProfiles()
	if err != nil || len(ps) != 1 || ps[0].Kind != "antigravity" || ps[0].Port != 3001 || ps[0].Username != "tobi" {
		t.Fatalf("profiles = %+v (err %v), want only the Antigravity one, kept", ps, err)
	}
}

// Rows stored under an outdated ID are renamed or merged on open, and the
// profile nodes pointing at them follow, so nothing is lost.
func TestStore_DedupeOnOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dedupe.db")
	store, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	mk := func(addr, id string) *model.ProxyConfig {
		c := &model.ProxyConfig{Protocol: model.ProtocolVLESS, Address: addr, Port: 443, UUID: "u", CreatedAt: time.Now(), UpdatedAt: time.Now()}
		c.ComputeID()
		if id != "" {
			c.ID = id
		}
		return c
	}
	a, aDup, bOld := mk("1.1.1.1", ""), mk("1.1.1.1", "old-a"), mk("2.2.2.2", "old-b")
	if _, _, err := store.UpsertConfigs([]*model.ProxyConfig{a, aDup, bOld}); err != nil {
		t.Fatal(err)
	}
	pid, _ := store.SaveProfile(Profile{Kind: "a", Name: "p", Port: 2000, Username: "u", Password: "p", Enabled: true})
	if err := store.ReplaceProfileNodes(pid, []ProfileNode{
		{ConfigID: "old-a", Rank: 1, OK: true, TestedAt: time.Now()},
		{ConfigID: "old-b", Rank: 2, OK: true, TestedAt: time.Now()},
	}); err != nil {
		t.Fatal(err)
	}
	store.Close()

	store, err = Open(path, nil) // dedupe runs here
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	all, _ := store.ListConfigsFiltered("", 0)
	if len(all) != 2 {
		t.Fatalf("want 2 configs after merge, got %d", len(all))
	}
	_, cfgs, err := store.ListProfileNodes(pid, true)
	bWant := mk("2.2.2.2", "").ID
	if err != nil || len(cfgs) != 2 || cfgs[0].ID != a.ID || cfgs[1].ID != bWant {
		t.Fatalf("profile nodes = %v (err %v), want [%s %s]", cfgs, err, a.ID, bWant)
	}
}
