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

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Configure for performance
	pragmas := []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA cache_size = -20000", // 20MB cache
		"PRAGMA foreign_keys = ON",
		"PRAGMA temp_store = MEMORY",
	}
	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("exec %q: %w", pragma, err)
		}
	}

	store := &Store{db: db, logger: logger}

	if err := store.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
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

		`CREATE TABLE IF NOT EXISTS test_results (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			config_id TEXT NOT NULL REFERENCES configs(id) ON DELETE CASCADE,
			stage INTEGER NOT NULL,
			success BOOLEAN NOT NULL,
			error TEXT,
			latency_ns INTEGER NOT NULL,
			ttfb_ns INTEGER,
			target_url TEXT,
			tested_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,

		`CREATE INDEX IF NOT EXISTS idx_results_config ON test_results(config_id)`,
		`CREATE INDEX IF NOT EXISTS idx_results_tested ON test_results(tested_at)`,

		`CREATE TABLE IF NOT EXISTS scores (
			config_id TEXT PRIMARY KEY REFERENCES configs(id) ON DELETE CASCADE,
			composite REAL NOT NULL DEFAULT 0,
			latency_score REAL NOT NULL DEFAULT 0,
			success_score REAL NOT NULL DEFAULT 0,
			throughput_score REAL NOT NULL DEFAULT 0,
			stability_penalty REAL NOT NULL DEFAULT 0,
			recency_bonus REAL NOT NULL DEFAULT 0,
			test_count INTEGER NOT NULL DEFAULT 0,
			last_tested_at DATETIME,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,

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

		`CREATE TABLE IF NOT EXISTS rotation_pool (
			config_id TEXT PRIMARY KEY REFERENCES configs(id) ON DELETE CASCADE,
			added_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,

		`CREATE INDEX IF NOT EXISTS idx_rotation_pool_added ON rotation_pool(added_at)`,
	}

	for _, m := range migrations {
		if _, err := s.db.Exec(m); err != nil {
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
			continue
		}

		_, err = stmt.Exec(cfg.ID, cfg.Name, cfg.RawURI, string(cfg.Protocol),
			cfg.Source, cfg.Address, cfg.Port, string(data), formatDBTime(cfg.CreatedAt), formatDBTime(cfg.UpdatedAt))
		if err != nil {
			continue
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

// Deduplicate scans all configs in the database, recalculates their canonical IDs
// using model.ProxyConfig.Hash(), removes duplicate entries, and cleans up orphaned scores/results.
func (s *Store) Deduplicate() (scanned, removed int, err error) {
	rows, err := s.db.Query(`SELECT id, data FROM configs`)
	if err != nil {
		return 0, 0, fmt.Errorf("query configs: %w", err)
	}
	defer rows.Close()

	type item struct {
		oldID string
		cfg   model.ProxyConfig
	}
	var all []item
	for rows.Next() {
		var oldID, data string
		if err := rows.Scan(&oldID, &data); err != nil {
			continue
		}
		var cfg model.ProxyConfig
		if err := json.Unmarshal([]byte(data), &cfg); err != nil {
			continue
		}
		all = append(all, item{oldID: oldID, cfg: cfg})
	}
	rows.Close()

	scanned = len(all)
	if scanned == 0 {
		return 0, 0, nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	seenCanonical := make(map[string]string) // canonicalID -> kept oldID
	var toDelete []string

	for _, it := range all {
		it.cfg.ComputeID()
		canonID := it.cfg.ID

		if keptOldID, exists := seenCanonical[canonID]; exists {
			// Duplicate found! Delete this redundant row
			toDelete = append(toDelete, it.oldID)
			s.logger.Debug("deduplicate: removing duplicate config", "duplicate_id", it.oldID, "kept_id", keptOldID)
		} else {
			seenCanonical[canonID] = it.oldID
			// If oldID != canonID, update ID in DB to canonical
			if it.oldID != canonID {
				data, _ := json.Marshal(it.cfg)
				_, err := tx.Exec(`UPDATE configs SET id = ?, data = ?, updated_at = ? WHERE id = ?`,
					canonID, string(data), formatDBTime(time.Now()), it.oldID)
				if err != nil {
					// Could be unique constraint if canonID was already present
					toDelete = append(toDelete, it.oldID)
				}
			}
		}
	}

	// Delete identified duplicate rows
	delStmt, err := tx.Prepare(`DELETE FROM configs WHERE id = ?`)
	if err == nil {
		defer delStmt.Close()
		for _, id := range toDelete {
			if _, err := delStmt.Exec(id); err == nil {
				removed++
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("commit deduplication: %w", err)
	}

	return scanned, removed, nil
}

// InsertTestResult stores a single test result.
func (s *Store) InsertTestResult(r *model.TestResult) error {
	_, err := s.db.Exec(`
		INSERT INTO test_results (config_id, stage, success, error, latency_ns, ttfb_ns, target_url, tested_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, r.ConfigID, int(r.Stage), r.Success, r.Error,
		r.Latency.Nanoseconds(), r.TTFB.Nanoseconds(), r.TargetURL, formatDBTime(r.TestedAt))
	return err
}

// InsertTestResults stores multiple test results in a batch.
func (s *Store) InsertTestResults(results []model.TestResult) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO test_results (config_id, stage, success, error, latency_ns, ttfb_ns, target_url, tested_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	for _, r := range results {
		_, err := stmt.Exec(r.ConfigID, int(r.Stage), r.Success, r.Error,
			r.Latency.Nanoseconds(), r.TTFB.Nanoseconds(), r.TargetURL, formatDBTime(r.TestedAt))
		if err != nil {
			s.logger.Warn("failed to insert test result", "error", err, "config_id", r.ConfigID)
		}
	}

	return tx.Commit()
}

// UpsertScore inserts or updates a config's score.
func (s *Store) UpsertScore(score *model.Score) error {
	_, err := s.db.Exec(`
		INSERT INTO scores (config_id, composite, latency_score, success_score, throughput_score,
			stability_penalty, recency_bonus, test_count, last_tested_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(config_id) DO UPDATE SET
			composite = excluded.composite,
			latency_score = excluded.latency_score,
			success_score = excluded.success_score,
			throughput_score = excluded.throughput_score,
			stability_penalty = excluded.stability_penalty,
			recency_bonus = excluded.recency_bonus,
			test_count = excluded.test_count,
			last_tested_at = excluded.last_tested_at,
			updated_at = excluded.updated_at
	`, score.ConfigID, score.Composite, score.LatencyScore, score.SuccessScore,
		score.ThroughputScore, score.StabilityPenalty, score.RecencyBonus,
		score.TestCount, formatDBTime(score.LastTestedAt), formatDBTime(score.UpdatedAt))
	return err
}

// ListConfigsFiltered returns configs ordered by score, optionally filtered by protocol.
func (s *Store) ListConfigsFiltered(protocol string, limit int) ([]*model.ProxyConfig, error) {
	if limit <= 0 {
		limit = -1
	}
	var query string
	var args []interface{}

	if protocol != "" {
		query = `
			SELECT c.data
			FROM configs c
			LEFT JOIN scores s ON c.id = s.config_id
			WHERE c.protocol = ?
			ORDER BY COALESCE(s.composite, 999999) ASC
			LIMIT ?
		`
		args = []interface{}{protocol, limit}
	} else {
		query = `
			SELECT c.data
			FROM configs c
			LEFT JOIN scores s ON c.id = s.config_id
			ORDER BY COALESCE(s.composite, 999999) ASC
			LIMIT ?
		`
		args = []interface{}{limit}
	}

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

// ListConfigsUntested returns configs that have never been tested (no score entry yet).
func (s *Store) ListConfigsUntested(protocol string, limit int) ([]*model.ProxyConfig, error) {
	if limit <= 0 {
		limit = -1
	}
	var query string
	var args []interface{}

	if protocol != "" {
		query = `
			SELECT c.data
			FROM configs c
			LEFT JOIN scores s ON c.id = s.config_id
			WHERE s.config_id IS NULL AND c.protocol = ?
			LIMIT ?
		`
		args = []interface{}{protocol, limit}
	} else {
		query = `
			SELECT c.data
			FROM configs c
			LEFT JOIN scores s ON c.id = s.config_id
			WHERE s.config_id IS NULL
			LIMIT ?
		`
		args = []interface{}{limit}
	}

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

// ListConfigs returns all configs ordered by score (best first).
func (s *Store) ListConfigs(limit int) ([]*model.ProxyConfig, error) {
	return s.ListConfigsFiltered("", limit)
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

// GetScore returns the score for a config.
func (s *Store) GetScore(configID string) (*model.Score, error) {
	var score model.Score
	var lastTestedStr, updatedStr sql.NullString
	err := s.db.QueryRow(`
		SELECT config_id, composite, latency_score, success_score, throughput_score,
			stability_penalty, recency_bonus, test_count, last_tested_at, updated_at
		FROM scores WHERE config_id = ?
	`, configID).Scan(
		&score.ConfigID, &score.Composite, &score.LatencyScore, &score.SuccessScore,
		&score.ThroughputScore, &score.StabilityPenalty, &score.RecencyBonus,
		&score.TestCount, &lastTestedStr, &updatedStr,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	score.LastTestedAt = parseDBTime(lastTestedStr.String)
	score.UpdatedAt = parseDBTime(updatedStr.String)
	return &score, nil
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

// DeleteConfig removes a config and its associated results and scores.
func (s *Store) DeleteConfig(id string) error {
	_, err := s.db.Exec("DELETE FROM configs WHERE id = ?", id)
	return err
}

// PurgeOldResults removes test results older than the given duration.
func (s *Store) PurgeOldResults(olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan)
	result, err := s.db.Exec("DELETE FROM test_results WHERE tested_at < ?", formatDBTime(cutoff))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
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
	TotalCount    int64            `json:"total_count"`
	LevelCounts   map[string]int64 `json:"level_counts"`
	OldestTime    *time.Time       `json:"oldest_time,omitempty"`
	NewestTime    *time.Time       `json:"newest_time,omitempty"`
	Retention     string           `json:"retention"`
	DBSizeBytes   int64            `json:"db_size_bytes"`
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

// SettingBenchmarkMethodology is the settings key for the test methodology chain.
const SettingBenchmarkMethodology = "benchmark_methodology"

// GetBenchmarkMethodology reads the saved benchmark methodology chain, returning DefaultMethodologyChain if not found.
func (s *Store) GetBenchmarkMethodology() (*model.BenchmarkMethodologyChain, error) {
	val, err := s.GetSetting(SettingBenchmarkMethodology, "")
	if err != nil || strings.TrimSpace(val) == "" {
		return model.DefaultMethodologyChain(), nil
	}
	chain, err := model.ParseMethodologyChain(val)
	if err != nil {
		return model.DefaultMethodologyChain(), nil
	}
	return chain, nil
}

// SaveBenchmarkMethodology validates and stores the benchmark methodology chain in the settings table.
func (s *Store) SaveBenchmarkMethodology(chain *model.BenchmarkMethodologyChain) error {
	if chain == nil {
		return fmt.Errorf("methodology chain cannot be nil")
	}
	if err := chain.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(chain)
	if err != nil {
		return fmt.Errorf("marshal methodology chain: %w", err)
	}
	return s.SetSetting(SettingBenchmarkMethodology, string(data))
}

// ConfigQueryFilter specifies parameters for filtering, sorting, and paginating configs.
type ConfigQueryFilter struct {
	Protocol    string
	Search      string
	WorkingOnly bool
	SortBy      string // "score", "latency", "name", "protocol", "updated"
	SortOrder   string // "asc", "desc"
	Limit       int
	Offset      int
}

// ConfigItem represents a proxy config enriched with score, latency, and pool membership.
type ConfigItem struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Protocol    string     `json:"protocol"`
	Address     string     `json:"address"`
	Port        int        `json:"port"`
	Network     string     `json:"network"`
	Security    string     `json:"security"`
	RawURI      string     `json:"raw_uri"`
	Score       *float64   `json:"score,omitempty"`
	LatencyMS   *float64   `json:"latency_ms,omitempty"`
	SuccessRate *float64   `json:"success_rate,omitempty"`
	LastTested  *time.Time `json:"last_tested_at,omitempty"`
	InPool      bool       `json:"in_pool"`
}

// QueryConfigs executes a high-performance, filtered, sorted, paginated query across configs.
func (s *Store) QueryConfigs(filter ConfigQueryFilter) ([]*ConfigItem, int64, error) {
	var conditions []string
	var args []any

	if filter.Protocol != "" && filter.Protocol != "all" {
		conditions = append(conditions, "c.protocol = ?")
		args = append(args, filter.Protocol)
	}

	if filter.WorkingOnly {
		conditions = append(conditions, "(s.composite IS NOT NULL AND s.composite < 999999 AND s.success_score == 0.0)")
	}

	if filter.Search != "" {
		conditions = append(conditions, "(c.name LIKE ? OR c.address LIKE ? OR CAST(c.port AS TEXT) LIKE ?)")
		pattern := "%" + filter.Search + "%"
		args = append(args, pattern, pattern, pattern)
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, " AND ")
	}

	// 1. Total matching count
	countQuery := fmt.Sprintf(`
		SELECT COUNT(*)
		FROM configs c
		LEFT JOIN scores s ON c.id = s.config_id
		LEFT JOIN rotation_pool rp ON c.id = rp.config_id
		%s
	`, whereClause)

	var total int64
	if err := s.db.QueryRow(countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count configs: %w", err)
	}

	// 2. Ordering
	orderDir := "ASC"
	if strings.ToLower(filter.SortOrder) == "desc" {
		orderDir = "DESC"
	}

	var orderBy string
	switch strings.ToLower(filter.SortBy) {
	case "latency":
		orderBy = fmt.Sprintf("(s.latency_score IS NULL) ASC, s.latency_score %s, c.id ASC", orderDir)
	case "name":
		orderBy = fmt.Sprintf("c.name %s, c.id ASC", orderDir)
	case "protocol":
		orderBy = fmt.Sprintf("c.protocol %s, c.id ASC", orderDir)
	case "updated":
		orderBy = fmt.Sprintf("(s.last_tested_at IS NULL) ASC, s.last_tested_at %s, c.id ASC", orderDir)
	default: // "score"
		orderBy = fmt.Sprintf("(s.composite IS NULL) ASC, s.composite %s, c.id ASC", orderDir)
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}

	dataQuery := fmt.Sprintf(`
		SELECT c.id, c.name, c.protocol, c.address, c.port, c.raw_uri, c.data,
		       s.composite, s.latency_score, s.success_score, s.last_tested_at,
		       (rp.config_id IS NOT NULL) AS in_pool
		FROM configs c
		LEFT JOIN scores s ON c.id = s.config_id
		LEFT JOIN rotation_pool rp ON c.id = rp.config_id
		%s
		ORDER BY %s
		LIMIT ? OFFSET ?
	`, whereClause, orderBy)

	queryArgs := append(args, limit, offset)
	rows, err := s.db.Query(dataQuery, queryArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("query configs: %w", err)
	}
	defer rows.Close()

	var items []*ConfigItem
	for rows.Next() {
		var item ConfigItem
		var rawData string
		var comp, lat, succ sql.NullFloat64
		var lastTestedStr sql.NullString
		var inPoolInt int

		if err := rows.Scan(
			&item.ID, &item.Name, &item.Protocol, &item.Address, &item.Port,
			&item.RawURI, &rawData, &comp, &lat, &succ, &lastTestedStr, &inPoolInt,
		); err != nil {
			return nil, 0, fmt.Errorf("scan config item: %w", err)
		}

		item.InPool = (inPoolInt == 1)

		if rawData != "" {
			var cfg model.ProxyConfig
			if err := json.Unmarshal([]byte(rawData), &cfg); err == nil {
				item.Network = string(cfg.Network)
				item.Security = string(cfg.Security)
				if item.Name == "" {
					item.Name = cfg.DisplayName()
				}
			}
		}

		if comp.Valid {
			v := comp.Float64
			item.Score = &v
		}
		if lat.Valid {
			v := lat.Float64
			item.LatencyMS = &v
		}
		if succ.Valid {
			// success_score = 0 means 100% success rate
			rate := (1.0 - succ.Float64) * 100.0
			if rate < 0 {
				rate = 0
			}
			item.SuccessRate = &rate
		}
		if lastTestedStr.Valid && lastTestedStr.String != "" {
			t := parseDBTime(lastTestedStr.String)
			item.LastTested = &t
		}

		items = append(items, &item)
	}

	return items, total, rows.Err()
}

// AddToRotationPool adds configuration IDs to the auto-rotation pool.
func (s *Store) AddToRotationPool(configIDs []string) (int64, error) {
	if len(configIDs) == 0 {
		return 0, nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare("INSERT OR IGNORE INTO rotation_pool (config_id) VALUES (?)")
	if err != nil {
		return 0, fmt.Errorf("prepare pool insert: %w", err)
	}
	defer stmt.Close()

	var added int64
	for _, id := range configIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		res, err := stmt.Exec(id)
		if err == nil {
			if n, _ := res.RowsAffected(); n > 0 {
				added += n
			}
		}
	}

	return added, tx.Commit()
}

// RemoveFromRotationPool removes configuration IDs from the auto-rotation pool.
func (s *Store) RemoveFromRotationPool(configIDs []string) (int64, error) {
	if len(configIDs) == 0 {
		return 0, nil
	}

	placeholders := make([]string, len(configIDs))
	args := make([]any, len(configIDs))
	for i, id := range configIDs {
		placeholders[i] = "?"
		args[i] = strings.TrimSpace(id)
	}

	res, err := s.db.Exec(fmt.Sprintf("DELETE FROM rotation_pool WHERE config_id IN (%s)", strings.Join(placeholders, ",")), args...)
	if err != nil {
		return 0, fmt.Errorf("delete from pool: %w", err)
	}
	return res.RowsAffected()
}

// ClearRotationPool removes all configurations from the auto-rotation pool.
func (s *Store) ClearRotationPool() error {
	_, err := s.db.Exec("DELETE FROM rotation_pool")
	return err
}

// AddWorkingConfigsToRotationPool inserts all configs with verified working test scores into the pool.
func (s *Store) AddWorkingConfigsToRotationPool(protocol string) (int64, error) {
	var query string
	var args []any

	if protocol != "" && protocol != "all" {
		query = `
			INSERT OR IGNORE INTO rotation_pool (config_id)
			SELECT c.id FROM configs c
			JOIN scores s ON c.id = s.config_id
			WHERE s.composite < 999999 AND s.success_score == 0.0 AND c.protocol = ?
		`
		args = append(args, protocol)
	} else {
		query = `
			INSERT OR IGNORE INTO rotation_pool (config_id)
			SELECT c.id FROM configs c
			JOIN scores s ON c.id = s.config_id
			WHERE s.composite < 999999 AND s.success_score == 0.0
		`
	}

	res, err := s.db.Exec(query, args...)
	if err != nil {
		return 0, fmt.Errorf("insert working to pool: %w", err)
	}
	return res.RowsAffected()
}

// GetRotationPoolConfigs returns all configurations currently in the rotation pool with their scores.
func (s *Store) GetRotationPoolConfigs() ([]*ConfigItem, error) {
	rows, err := s.db.Query(`
		SELECT c.id, c.name, c.protocol, c.address, c.port, c.raw_uri, c.data,
		       s.composite, s.latency_score, s.success_score, s.last_tested_at
		FROM rotation_pool rp
		JOIN configs c ON rp.config_id = c.id
		LEFT JOIN scores s ON c.id = s.config_id
		ORDER BY (s.composite IS NULL) ASC, s.composite ASC, c.id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("query rotation pool: %w", err)
	}
	defer rows.Close()

	var items []*ConfigItem
	for rows.Next() {
		var item ConfigItem
		var rawData string
		var comp, lat, succ sql.NullFloat64
		var lastTestedStr sql.NullString

		if err := rows.Scan(
			&item.ID, &item.Name, &item.Protocol, &item.Address, &item.Port,
			&item.RawURI, &rawData, &comp, &lat, &succ, &lastTestedStr,
		); err != nil {
			return nil, fmt.Errorf("scan pool config item: %w", err)
		}

		item.InPool = true

		if rawData != "" {
			var cfg model.ProxyConfig
			if err := json.Unmarshal([]byte(rawData), &cfg); err == nil {
				item.Network = string(cfg.Network)
				item.Security = string(cfg.Security)
				if item.Name == "" {
					item.Name = cfg.DisplayName()
				}
			}
		}

		if comp.Valid {
			v := comp.Float64
			item.Score = &v
		}
		if lat.Valid {
			v := lat.Float64
			item.LatencyMS = &v
		}
		if succ.Valid {
			rate := (1.0 - succ.Float64) * 100.0
			if rate < 0 {
				rate = 0
			}
			item.SuccessRate = &rate
		}
		if lastTestedStr.Valid && lastTestedStr.String != "" {
			t := parseDBTime(lastTestedStr.String)
			item.LastTested = &t
		}

		items = append(items, &item)
	}

	return items, rows.Err()
}

// GetRotationPoolStats returns the total number of items in the pool and how many are working.
func (s *Store) GetRotationPoolStats() (total int64, working int64, err error) {
	err = s.db.QueryRow(`
		SELECT COUNT(*),
		       COALESCE(SUM(CASE WHEN s.composite IS NOT NULL AND s.composite < 999999 AND s.success_score == 0.0 THEN 1 ELSE 0 END), 0)
		FROM rotation_pool rp
		JOIN configs c ON rp.config_id = c.id
		LEFT JOIN scores s ON c.id = s.config_id
	`).Scan(&total, &working)
	return total, working, err
}

