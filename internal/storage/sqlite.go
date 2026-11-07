// Package storage provides SQLite-based persistence for proxy configs,
// test results, and scores. Uses WAL mode for concurrent read/write.
package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/AmirAM03/velox/internal/model"

	_ "modernc.org/sqlite"
)

// Store is the SQLite storage backend.
type Store struct {
	db     *sql.DB
	logger *slog.Logger
}

// Open creates a new Store, initializing the database and running migrations.
func Open(dbPath string, logger *slog.Logger) (*Store, error) {
	if logger == nil {
		logger = slog.Default()
	}

	// Pragmas go in the DSN so every pooled connection gets them, not just the first one.
	dsn := dbPath + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)" +
		"&_pragma=cache_size(-20000)&_pragma=foreign_keys(1)&_pragma=temp_store(MEMORY)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	store := &Store{db: db, logger: logger}

	if err := store.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	// New configs are deduplicated on ingest (canonical ID as primary key). This pass only
	// fixes rows stored under an older ID rule, so it runs once per start, not per fetch.
	if scanned, merged, err := store.dedupe(); err != nil {
		logger.Warn("startup deduplication failed", "source", "storage", "error", err)
	} else if merged > 0 {
		logger.Info("startup deduplication", "source", "storage", "scanned", scanned, "merged", merged)
	}

	return store, nil
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// migrate runs database schema migrations.
func (s *Store) migrate() error {
	migrations := []string{
		`CREATE TABLE IF NOT EXISTS configs (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL DEFAULT '',
			raw_uri TEXT NOT NULL,
			protocol TEXT NOT NULL,
			source TEXT NOT NULL DEFAULT '',
			address TEXT NOT NULL,
			port INTEGER NOT NULL,
			data TEXT NOT NULL,  -- JSON blob of full ProxyConfig
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,

		`CREATE INDEX IF NOT EXISTS idx_configs_protocol ON configs(protocol)`,
		`CREATE INDEX IF NOT EXISTS idx_configs_address ON configs(address)`,

		// test_results was written on every benchmark but never read; drop it so old DBs stop growing.
		`DROP TABLE IF EXISTS test_results`,

		// Scores belonged to the removed manual benchmark; profiles keep their own ranking.
		`DROP TABLE IF EXISTS scores`,

		`CREATE TABLE IF NOT EXISTS app_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			level TEXT NOT NULL,
			source TEXT NOT NULL,
			message TEXT NOT NULL,
			attrs TEXT NOT NULL DEFAULT '{}'
		)`,

		`CREATE INDEX IF NOT EXISTS idx_logs_timestamp ON app_logs(timestamp)`,
		`CREATE INDEX IF NOT EXISTS idx_logs_level ON app_logs(level)`,
		`CREATE INDEX IF NOT EXISTS idx_logs_source ON app_logs(source)`,

		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,

		// The manual rotation pool was replaced by per-profile node selection.
		`DROP TABLE IF EXISTS rotation_pool`,

		`CREATE TABLE IF NOT EXISTS sources (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			url TEXT NOT NULL UNIQUE,
			enabled BOOLEAN NOT NULL DEFAULT 1,
			last_fetched_at DATETIME,
			last_error TEXT NOT NULL DEFAULT '',
			last_count INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,

		`CREATE TABLE IF NOT EXISTS profiles (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			port INTEGER NOT NULL UNIQUE,
			username TEXT NOT NULL,
			password TEXT NOT NULL,
			enabled BOOLEAN NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,

		`CREATE TABLE IF NOT EXISTS profile_nodes (
			profile_id INTEGER NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
			config_id TEXT NOT NULL REFERENCES configs(id) ON DELETE CASCADE,
			rank INTEGER NOT NULL,
			latency_ms REAL NOT NULL,
			tested_at DATETIME NOT NULL,
			PRIMARY KEY (profile_id, config_id)
		)`,
	}

	for _, m := range migrations {
		if _, err := s.db.Exec(m); err != nil {
			return fmt.Errorf("migration failed: %w\nSQL: %s", err, m)
		}
	}

	// Column changes after release. SQLite has no IF [NOT] EXISTS for columns, so
	// "already done" errors are expected on every start after the first.
	for _, m := range []string{
		`ALTER TABLE profiles ADD COLUMN auto_test BOOLEAN NOT NULL DEFAULT 1`,
		`ALTER TABLE profiles ADD COLUMN test_interval TEXT NOT NULL DEFAULT '30m'`,
		`ALTER TABLE profiles ADD COLUMN kind TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE profiles ADD COLUMN settings TEXT NOT NULL DEFAULT '{}'`,
		`ALTER TABLE profile_nodes ADD COLUMN note TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE profile_nodes ADD COLUMN ok BOOLEAN NOT NULL DEFAULT 1`,
		`ALTER TABLE profile_nodes ADD COLUMN error TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE profile_nodes ADD COLUMN response TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE profile_nodes DROP COLUMN speed_bps`,
		// Free-form target-URL profiles became built-in profile types. An old Antigravity
		// profile becomes the new one, so its port and credentials keep working.
		`UPDATE profiles SET kind = 'antigravity'
			WHERE id = (SELECT MIN(id) FROM profiles WHERE kind = '' AND target_url LIKE '%antigravity%')
			AND NOT EXISTS (SELECT 1 FROM profiles WHERE kind = 'antigravity')`,
		`ALTER TABLE profiles DROP COLUMN target_url`,
		`DELETE FROM profiles WHERE kind = ''`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_profiles_kind ON profiles(kind)`,
	} {
		if _, err := s.db.Exec(m); err != nil && !strings.Contains(err.Error(), "duplicate column") && !strings.Contains(err.Error(), "no such column") {
			return fmt.Errorf("migration failed: %w\nSQL: %s", err, m)
		}
	}

	s.logger.Debug("database migrations complete")
	return nil
}

func formatDBTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339Nano)
}

func parseDBTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	formats := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// UpsertConfig inserts or updates a proxy config.
func (s *Store) UpsertConfig(cfg *model.ProxyConfig) error {
	data, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	_, err = s.db.Exec(`
		INSERT INTO configs (id, name, raw_uri, protocol, source, address, port, data, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			raw_uri = excluded.raw_uri,
			source = excluded.source,
			data = excluded.data,
			updated_at = excluded.updated_at
	`, cfg.ID, cfg.Name, cfg.RawURI, string(cfg.Protocol), cfg.Source,
		cfg.Address, cfg.Port, string(data), formatDBTime(cfg.CreatedAt), formatDBTime(cfg.UpdatedAt))

	return err
}

// UpsertConfigs inserts or updates multiple configs in a single transaction.
// It accurately detects which configs are brand new vs existing/updated.
func (s *Store) UpsertConfigs(configs []*model.ProxyConfig) (inserted, updated int, err error) {
	if len(configs) == 0 {
		return 0, 0, nil
	}

	// 1. Identify which IDs already exist in the database (in chunks of 500)
	existingIDs := make(map[string]bool)
	for i := 0; i < len(configs); i += 500 {
		end := i + 500
		if end > len(configs) {
			end = len(configs)
		}
		chunk := configs[i:end]
		placeholders := make([]string, len(chunk))
		args := make([]interface{}, len(chunk))
		for j, cfg := range chunk {
			placeholders[j] = "?"
			args[j] = cfg.ID
		}
		query := fmt.Sprintf("SELECT id FROM configs WHERE id IN (%s)", strings.Join(placeholders, ","))
		rows, err := s.db.Query(query, args...)
		if err == nil {
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err == nil {
					existingIDs[id] = true
				}
			}
			rows.Close()
		}
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO configs (id, name, raw_uri, protocol, source, address, port, data, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = CASE WHEN excluded.name != '' AND excluded.name != configs.name THEN excluded.name ELSE configs.name END,
			raw_uri = excluded.raw_uri,
			source = excluded.source,
			data = excluded.data,
			updated_at = excluded.updated_at
	`)
	if err != nil {
		return 0, 0, fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	for _, cfg := range configs {
		data, err := json.Marshal(cfg)
		if err != nil {
			return 0, 0, fmt.Errorf("marshal config %s: %w", cfg.ID, err)
		}

		_, err = stmt.Exec(cfg.ID, cfg.Name, cfg.RawURI, string(cfg.Protocol),
			cfg.Source, cfg.Address, cfg.Port, string(data), formatDBTime(cfg.CreatedAt), formatDBTime(cfg.UpdatedAt))
		if err != nil {
			return 0, 0, fmt.Errorf("upsert config %s: %w", cfg.ID, err)
		}

		if existingIDs[cfg.ID] {
			updated++
		} else {
			inserted++
			existingIDs[cfg.ID] = true // prevent duplicate in same batch from double-counting
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("commit: %w", err)
	}

	return inserted, updated, nil
}

// dedupe rewrites every config to its current canonical ID and merges rows that
// share one. References (profile_nodes) move with the row, so a profile
// keeps its nodes and no config is lost.
func (s *Store) dedupe() (scanned, merged int, err error) {
	rows, err := s.db.Query(`SELECT id, data FROM configs`)
	if err != nil {
		return 0, 0, fmt.Errorf("query configs: %w", err)
	}
	type item struct {
		oldID, newID string
		data         []byte
	}
	var stale []item
	present := map[string]bool{}
	for rows.Next() {
		var id, data string
		if err := rows.Scan(&id, &data); err != nil {
			rows.Close()
			return 0, 0, err
		}
		scanned++
		present[id] = true
		var cfg model.ProxyConfig
		if json.Unmarshal([]byte(data), &cfg) != nil {
			continue // unreadable row: leave it, never delete what we cannot parse
		}
		cfg.ComputeID()
		if cfg.ID != id {
			b, _ := json.Marshal(cfg)
			stale = append(stale, item{id, cfg.ID, b})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	if len(stale) == 0 {
		return scanned, 0, nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	// Keys change in several tables inside this transaction; check them at commit.
	if _, err := tx.Exec(`PRAGMA defer_foreign_keys = ON`); err != nil {
		return 0, 0, err
	}
	for _, it := range stale {
		if present[it.newID] {
			// Another row already holds this identity: it wins, this one merges into it.
			merged++
		} else {
			if _, err := tx.Exec(`UPDATE configs SET id = ?, data = ? WHERE id = ?`, it.newID, string(it.data), it.oldID); err != nil {
				return 0, 0, fmt.Errorf("rename config: %w", err)
			}
			present[it.newID] = true
		}
		// Move references; where the target already has one, the old one is dropped below.
		if _, err := tx.Exec(`UPDATE OR IGNORE profile_nodes SET config_id = ? WHERE config_id = ?`, it.newID, it.oldID); err != nil {
			return 0, 0, fmt.Errorf("move references: %w", err)
		}
		if _, err := tx.Exec(`DELETE FROM configs WHERE id = ?`, it.oldID); err != nil { // no-op after a rename
			return 0, 0, fmt.Errorf("delete duplicate: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("commit deduplication: %w", err)
	}
	return scanned, merged, nil
}

// ListConfigsFiltered returns stored configs, optionally of one protocol. limit <= 0 means all.
func (s *Store) ListConfigsFiltered(protocol string, limit int) ([]*model.ProxyConfig, error) {
	if limit <= 0 {
		limit = -1
	}
	query := `SELECT data FROM configs WHERE (? = '' OR protocol = ?) ORDER BY id LIMIT ?`
	args := []interface{}{protocol, protocol, limit}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var configs []*model.ProxyConfig
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			continue
		}
		var cfg model.ProxyConfig
		if err := json.Unmarshal([]byte(data), &cfg); err != nil {
			continue
		}
		configs = append(configs, &cfg)
	}
	return configs, rows.Err()
}

// GetConfigsByIDs returns configs matching the given IDs.
func (s *Store) GetConfigsByIDs(ids []string) ([]*model.ProxyConfig, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}

	rows, err := s.db.Query(
		fmt.Sprintf("SELECT data FROM configs WHERE id IN (%s)", strings.Join(placeholders, ",")),
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var configs []*model.ProxyConfig
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			continue
		}
		var cfg model.ProxyConfig
		if err := json.Unmarshal([]byte(data), &cfg); err != nil {
			continue
		}
		configs = append(configs, &cfg)
	}
	return configs, rows.Err()
}

// ConfigCount returns the total number of stored configs.
func (s *Store) ConfigCount() (int, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM configs").Scan(&count)
	return count, err
}

// ConfigCountByProtocol returns config counts grouped by protocol.
func (s *Store) ConfigCountByProtocol() (map[string]int, error) {
	rows, err := s.db.Query("SELECT protocol, COUNT(*) FROM configs GROUP BY protocol")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var protocol string
		var count int
		if err := rows.Scan(&protocol, &count); err != nil {
			continue
		}
		counts[protocol] = count
	}
	return counts, rows.Err()
}

// DeleteConfig removes a config.
func (s *Store) DeleteConfig(id string) error {
	_, err := s.db.Exec("DELETE FROM configs WHERE id = ?", id)
	return err
}

// LogRecord represents a persistent application log entry.
type LogRecord struct {
	ID        int64          `json:"id"`
	Timestamp time.Time      `json:"timestamp"`
	Level     string         `json:"level"`
	Source    string         `json:"source"`
	Message   string         `json:"message"`
	Attrs     map[string]any `json:"attrs,omitempty"`
	AttrsJSON string         `json:"attrs_json,omitempty"`
}

// LogQueryFilter specifies query parameters for filtering application logs.
type LogQueryFilter struct {
	Level  string
	Source string
	Search string
	Since  time.Time
	Until  time.Time
	Limit  int
	Offset int
}

// LogStats provides aggregated statistics about stored application logs.
type LogStats struct {
	TotalCount  int64            `json:"total_count"`
	LevelCounts map[string]int64 `json:"level_counts"`
	OldestTime  *time.Time       `json:"oldest_time,omitempty"`
	NewestTime  *time.Time       `json:"newest_time,omitempty"`
	Retention   string           `json:"retention"`
	DBSizeBytes int64            `json:"db_size_bytes"`
}

// InsertLog inserts a single log record.
func (s *Store) InsertLog(rec *LogRecord) error {
	return s.InsertLogsBatch([]*LogRecord{rec})
}

// InsertLogsBatch inserts multiple log records efficiently within a single transaction.
func (s *Store) InsertLogsBatch(records []*LogRecord) error {
	if len(records) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO app_logs (timestamp, level, source, message, attrs)
		VALUES (?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare insert log: %w", err)
	}
	defer stmt.Close()

	for _, r := range records {
		ts := r.Timestamp
		if ts.IsZero() {
			ts = time.Now()
		}
		attrsJSON := r.AttrsJSON
		if attrsJSON == "" && len(r.Attrs) > 0 {
			if b, err := json.Marshal(r.Attrs); err == nil {
				attrsJSON = string(b)
			}
		}
		if attrsJSON == "" {
			attrsJSON = "{}"
		}

		if _, err := stmt.Exec(formatDBTime(ts), r.Level, r.Source, r.Message, attrsJSON); err != nil {
			return fmt.Errorf("exec insert log: %w", err)
		}
	}

	return tx.Commit()
}

// QueryLogs searches logs matching the filter and returns the records and total count.
func (s *Store) QueryLogs(filter LogQueryFilter) ([]*LogRecord, int64, error) {
	var conditions []string
	var args []any

	if filter.Level != "" && filter.Level != "all" {
		conditions = append(conditions, "level = ?")
		args = append(args, strings.ToUpper(filter.Level))
	}
	if filter.Source != "" && filter.Source != "all" {
		conditions = append(conditions, "source = ?")
		args = append(args, filter.Source)
	}
	if filter.Search != "" {
		conditions = append(conditions, "(message LIKE ? OR attrs LIKE ?)")
		pattern := "%" + filter.Search + "%"
		args = append(args, pattern, pattern)
	}
	if !filter.Since.IsZero() {
		conditions = append(conditions, "timestamp >= ?")
		args = append(args, formatDBTime(filter.Since))
	}
	if !filter.Until.IsZero() {
		conditions = append(conditions, "timestamp <= ?")
		args = append(args, formatDBTime(filter.Until))
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, " AND ")
	}

	// 1. Count total matching rows
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM app_logs %s", whereClause)
	var totalCount int64
	if err := s.db.QueryRow(countQuery, args...).Scan(&totalCount); err != nil {
		return nil, 0, fmt.Errorf("count logs: %w", err)
	}

	// 2. Fetch page rows
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	} else if limit > 1000 {
		limit = 1000
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}

	dataQuery := fmt.Sprintf(`
		SELECT id, timestamp, level, source, message, attrs
		FROM app_logs %s
		ORDER BY timestamp DESC, id DESC
		LIMIT ? OFFSET ?
	`, whereClause)

	queryArgs := append(args, limit, offset)
	rows, err := s.db.Query(dataQuery, queryArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("query logs: %w", err)
	}
	defer rows.Close()

	var records []*LogRecord
	for rows.Next() {
		var r LogRecord
		var tsStr, attrsStr string
		if err := rows.Scan(&r.ID, &tsStr, &r.Level, &r.Source, &r.Message, &attrsStr); err != nil {
			return nil, 0, fmt.Errorf("scan log row: %w", err)
		}
		r.Timestamp = parseDBTime(tsStr)
		r.AttrsJSON = attrsStr
		if attrsStr != "" && attrsStr != "{}" {
			var attrs map[string]any
			if err := json.Unmarshal([]byte(attrsStr), &attrs); err == nil {
				r.Attrs = attrs
			}
		}
		records = append(records, &r)
	}

	return records, totalCount, rows.Err()
}

// PruneLogs deletes log entries older than the specified duration.
func (s *Store) PruneLogs(olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan)
	res, err := s.db.Exec("DELETE FROM app_logs WHERE timestamp < ?", formatDBTime(cutoff))
	if err != nil {
		return 0, fmt.Errorf("prune logs: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}

	// Reclaim free space periodically
	_, _ = s.db.Exec("PRAGMA wal_checkpoint(PASSIVE)")
	return affected, nil
}

// ClearLogs deletes all log records and reclaims database space.
func (s *Store) ClearLogs() error {
	if _, err := s.db.Exec("DELETE FROM app_logs"); err != nil {
		return fmt.Errorf("delete logs: %w", err)
	}
	_, _ = s.db.Exec("VACUUM")
	return nil
}

// GetLogStats returns statistical information about stored application logs.
func (s *Store) GetLogStats() (*LogStats, error) {
	stats := &LogStats{
		LevelCounts: make(map[string]int64),
		Retention:   "7d",
	}

	// Total count
	if err := s.db.QueryRow("SELECT COUNT(*) FROM app_logs").Scan(&stats.TotalCount); err != nil {
		return nil, fmt.Errorf("count logs: %w", err)
	}

	// Level counts
	levelRows, err := s.db.Query("SELECT level, COUNT(*) FROM app_logs GROUP BY level")
	if err == nil {
		defer levelRows.Close()
		for levelRows.Next() {
			var lvl string
			var cnt int64
			if err := levelRows.Scan(&lvl, &cnt); err == nil {
				stats.LevelCounts[lvl] = cnt
			}
		}
	}

	// Min / Max timestamps
	var minStr, maxStr sql.NullString
	if err := s.db.QueryRow("SELECT MIN(timestamp), MAX(timestamp) FROM app_logs").Scan(&minStr, &maxStr); err == nil {
		if minStr.Valid && minStr.String != "" {
			t := parseDBTime(minStr.String)
			stats.OldestTime = &t
		}
		if maxStr.Valid && maxStr.String != "" {
			t := parseDBTime(maxStr.String)
			stats.NewestTime = &t
		}
	}

	// Retention setting
	ret, err := s.GetSetting("log_retention", "7d")
	if err == nil && ret != "" {
		stats.Retention = ret
	}

	// DB Size in bytes: page_count * page_size
	var pageCount, pageSize int64
	if err := s.db.QueryRow("PRAGMA page_count").Scan(&pageCount); err == nil {
		if err := s.db.QueryRow("PRAGMA page_size").Scan(&pageSize); err == nil {
			stats.DBSizeBytes = pageCount * pageSize
		}
	}

	return stats, nil
}

// GetSetting reads a key from the settings table, returning defaultValue if not found.
func (s *Store) GetSetting(key, defaultValue string) (string, error) {
	var val string
	err := s.db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&val)
	if err == sql.ErrNoRows {
		return defaultValue, nil
	}
	if err != nil {
		return defaultValue, err
	}
	return val, nil
}

// SetSetting writes or updates a setting key-value pair.
func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`
		INSERT INTO settings (key, value, updated_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(key) DO UPDATE SET
			value = excluded.value,
			updated_at = excluded.updated_at
	`, key, value)
	return err
}

// ConfigQueryFilter specifies parameters for filtering, sorting, and paginating configs.
type ConfigQueryFilter struct {
	Protocol string
	Search   string
	SortBy   string // "updated" (default, newest first), "name", "protocol"
	Limit    int
	Offset   int
}

// ConfigItem is one stored config as the dashboard lists it.
type ConfigItem struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Protocol  string    `json:"protocol"`
	Address   string    `json:"address"`
	Port      int       `json:"port"`
	Network   string    `json:"network"`
	Security  string    `json:"security"`
	RawURI    string    `json:"raw_uri"`
	Source    string    `json:"source"`
	UpdatedAt time.Time `json:"updated_at"` // last time a source listed it
}

// QueryConfigs returns one filtered, sorted page of configs and the total match count.
func (s *Store) QueryConfigs(filter ConfigQueryFilter) ([]*ConfigItem, int64, error) {
	where := `WHERE (? = '' OR protocol = ?)`
	args := []any{filter.Protocol, filter.Protocol}
	if filter.Search != "" {
		where += ` AND (name LIKE ? OR address LIKE ? OR CAST(port AS TEXT) LIKE ?)`
		pattern := "%" + filter.Search + "%"
		args = append(args, pattern, pattern, pattern)
	}

	var total int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM configs `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count configs: %w", err)
	}

	orderBy := "updated_at DESC, id" // fixed strings only: never user input in ORDER BY
	switch filter.SortBy {
	case "name":
		orderBy = "name, id"
	case "protocol":
		orderBy = "protocol, id"
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}

	rows, err := s.db.Query(`SELECT id, name, protocol, address, port, raw_uri, source, data, updated_at FROM configs `+
		where+` ORDER BY `+orderBy+` LIMIT ? OFFSET ?`, append(args, limit, max(filter.Offset, 0))...)
	if err != nil {
		return nil, 0, fmt.Errorf("query configs: %w", err)
	}
	defer rows.Close()

	items := []*ConfigItem{}
	for rows.Next() {
		var item ConfigItem
		var data, updated string
		if err := rows.Scan(&item.ID, &item.Name, &item.Protocol, &item.Address, &item.Port,
			&item.RawURI, &item.Source, &data, &updated); err != nil {
			return nil, 0, fmt.Errorf("scan config item: %w", err)
		}
		var cfg model.ProxyConfig
		if json.Unmarshal([]byte(data), &cfg) == nil {
			item.Network, item.Security = string(cfg.Network), string(cfg.Security)
			if item.Name == "" {
				item.Name = cfg.DisplayName()
			}
		}
		item.UpdatedAt = parseDBTime(updated)
		items = append(items, &item)
	}
	return items, total, rows.Err()
}
