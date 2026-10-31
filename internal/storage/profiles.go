package storage

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/AmirAM03/velox/internal/model"
)

// Source is a subscription URL that Velox fetches configs from.
type Source struct {
	ID            int64      `json:"id"`
	URL           string     `json:"url"`
	Enabled       bool       `json:"enabled"`
	LastFetchedAt *time.Time `json:"last_fetched_at,omitempty"`
	LastError     string     `json:"last_error"`
	LastCount     int        `json:"last_count"`
}

// Profile is a public proxy port bound to one target service.
type Profile struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	TargetURL string `json:"target_url"`
	Port      int    `json:"port"`
	Username  string `json:"username"`
	Password  string `json:"password"`
	Enabled   bool   `json:"enabled"`
}

// ProfileNode is one ranked config chosen for a profile by the last test cycle.
type ProfileNode struct {
	ConfigID  string    `json:"config_id"`
	Rank      int       `json:"rank"`
	LatencyMS float64   `json:"latency_ms"`
	TestedAt  time.Time `json:"tested_at"`
}

// ListSources returns all subscription sources.
func (s *Store) ListSources() ([]Source, error) {
	rows, err := s.db.Query(`SELECT id, url, enabled, last_fetched_at, last_error, last_count FROM sources ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Source{}
	for rows.Next() {
		var src Source
		var fetched sql.NullString
		if err := rows.Scan(&src.ID, &src.URL, &src.Enabled, &fetched, &src.LastError, &src.LastCount); err != nil {
			return nil, err
		}
		if fetched.Valid && fetched.String != "" {
			t := parseDBTime(fetched.String)
			src.LastFetchedAt = &t
		}
		out = append(out, src)
	}
	return out, rows.Err()
}

// AddSource stores a new subscription URL.
func (s *Store) AddSource(url string) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO sources (url) VALUES (?)`, url)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// SetSourceEnabled turns a source on or off.
func (s *Store) SetSourceEnabled(id int64, enabled bool) error {
	return s.execOne(`UPDATE sources SET enabled = ? WHERE id = ?`, enabled, id)
}

// DeleteSource removes a source. Its configs stay until they age out.
func (s *Store) DeleteSource(id int64) error {
	return s.execOne(`DELETE FROM sources WHERE id = ?`, id)
}

// RecordSourceFetch stores the outcome of one fetch.
func (s *Store) RecordSourceFetch(id int64, count int, fetchErr error) error {
	msg := ""
	if fetchErr != nil {
		msg = fetchErr.Error()
	}
	_, err := s.db.Exec(`UPDATE sources SET last_fetched_at = ?, last_error = ?, last_count = ? WHERE id = ?`,
		formatDBTime(time.Now()), msg, count, id)
	return err
}

// PruneStaleConfigs deletes configs no source has listed since the cutoff,
// except ones a profile is currently using.
func (s *Store) PruneStaleConfigs(olderThan time.Duration) (int64, error) {
	res, err := s.db.Exec(`
		DELETE FROM configs
		WHERE updated_at < ? AND id NOT IN (SELECT config_id FROM profile_nodes)`,
		formatDBTime(time.Now().Add(-olderThan)))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ListProfiles returns all profiles ordered by port.
func (s *Store) ListProfiles() ([]Profile, error) {
	rows, err := s.db.Query(`SELECT id, name, target_url, port, username, password, enabled FROM profiles ORDER BY port`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Profile{}
	for rows.Next() {
		var p Profile
		if err := rows.Scan(&p.ID, &p.Name, &p.TargetURL, &p.Port, &p.Username, &p.Password, &p.Enabled); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SaveProfile inserts the profile when ID is 0, otherwise updates it. It returns the ID.
func (s *Store) SaveProfile(p Profile) (int64, error) {
	if p.ID == 0 {
		res, err := s.db.Exec(`INSERT INTO profiles (name, target_url, port, username, password, enabled) VALUES (?, ?, ?, ?, ?, ?)`,
			p.Name, p.TargetURL, p.Port, p.Username, p.Password, p.Enabled)
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	return p.ID, s.execOne(`UPDATE profiles SET name = ?, target_url = ?, port = ?, username = ?, password = ?, enabled = ? WHERE id = ?`,
		p.Name, p.TargetURL, p.Port, p.Username, p.Password, p.Enabled, p.ID)
}

// DeleteProfile removes a profile and its ranked nodes.
func (s *Store) DeleteProfile(id int64) error {
	return s.execOne(`DELETE FROM profiles WHERE id = ?`, id)
}

// ReplaceProfileNodes swaps a profile's ranked nodes for a new set in one transaction.
func (s *Store) ReplaceProfileNodes(profileID int64, nodes []ProfileNode) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM profile_nodes WHERE profile_id = ?`, profileID); err != nil {
		return err
	}
	for _, n := range nodes {
		if _, err := tx.Exec(`INSERT INTO profile_nodes (profile_id, config_id, rank, latency_ms, tested_at) VALUES (?, ?, ?, ?, ?)`,
			profileID, n.ConfigID, n.Rank, n.LatencyMS, formatDBTime(n.TestedAt)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListProfileNodes returns a profile's ranked nodes with their configs, best first.
func (s *Store) ListProfileNodes(profileID int64) ([]ProfileNode, []*model.ProxyConfig, error) {
	rows, err := s.db.Query(`
		SELECT pn.config_id, pn.rank, pn.latency_ms, pn.tested_at
		FROM profile_nodes pn WHERE pn.profile_id = ? ORDER BY pn.rank`, profileID)
	if err != nil {
		return nil, nil, err
	}
	var nodes []ProfileNode
	var ids []string
	for rows.Next() {
		var n ProfileNode
		var tested string
		if err := rows.Scan(&n.ConfigID, &n.Rank, &n.LatencyMS, &tested); err != nil {
			rows.Close()
			return nil, nil, err
		}
		n.TestedAt = parseDBTime(tested)
		nodes = append(nodes, n)
		ids = append(ids, n.ConfigID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	// GetConfigsByIDs does not keep order, so put configs back in rank order.
	cfgs, err := s.GetConfigsByIDs(ids)
	if err != nil {
		return nil, nil, err
	}
	byID := make(map[string]*model.ProxyConfig, len(cfgs))
	for _, c := range cfgs {
		byID[c.ID] = c
	}
	ordered := make([]*model.ProxyConfig, 0, len(nodes))
	for _, n := range nodes {
		if c := byID[n.ConfigID]; c != nil {
			ordered = append(ordered, c)
		}
	}
	return nodes, ordered, nil
}

// execOne runs a statement that must touch exactly one row.
func (s *Store) execOne(query string, args ...any) error {
	res, err := s.db.Exec(query, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("not found")
	}
	return nil
}
