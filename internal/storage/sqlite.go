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
