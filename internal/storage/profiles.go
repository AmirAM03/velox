package storage

import (
	"database/sql"
	"encoding/json"
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

// Profile is a public proxy port bound to one built-in profile type (checks.Kind).
type Profile struct {
	ID           int64             `json:"id"`
	Kind         string            `json:"kind"`
	Name         string            `json:"name"`
	Port         int               `json:"port"`
	Username     string            `json:"username"`
	Password     string            `json:"password"`
	Enabled      bool              `json:"enabled"`
	AutoTest     bool              `json:"auto_test"`     // re-tested automatically on its own interval; off = only "Test now"
	TestInterval string            `json:"test_interval"` // how often auto-test runs, e.g. "30m"
	Settings     map[string]string `json:"settings"`      // the type's own fields, e.g. a session credential
}

// ProfileNode is one config the profile's real check looked at in the last test:
// the passing ones are the profile's candidates, ranked best first (Rank 1..n);
// the others keep their label and why they failed (Rank 0).
type ProfileNode struct {
	ConfigID  string    `json:"config_id"`
	Rank      int       `json:"rank"`
	OK        bool      `json:"ok"`         // passed the check: a candidate the profile may serve
	LatencyMS float64   `json:"latency_ms"` // response time of the check's deciding request
	Label     string    `json:"label"`      // what the check saw, e.g. the country the service placed the node in
	Error     string    `json:"error,omitempty"`
	Response  string    `json:"response,omitempty"` // raw body of the check's deciding response
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
// except a profile's current candidates. Failed check results go with their config.
func (s *Store) PruneStaleConfigs(olderThan time.Duration) (int64, error) {
	res, err := s.db.Exec(`
		DELETE FROM configs
		WHERE updated_at < ? AND id NOT IN (SELECT config_id FROM profile_nodes WHERE ok = 1)`,
		formatDBTime(time.Now().Add(-olderThan)))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ListProfiles returns all profiles ordered by port.
func (s *Store) ListProfiles() ([]Profile, error) {
	rows, err := s.db.Query(`SELECT id, kind, name, port, username, password, enabled, auto_test, test_interval, settings FROM profiles ORDER BY port`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Profile{}
	for rows.Next() {
		var p Profile
		var settings string
		if err := rows.Scan(&p.ID, &p.Kind, &p.Name, &p.Port, &p.Username, &p.Password, &p.Enabled, &p.AutoTest, &p.TestInterval, &settings); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(settings), &p.Settings); err != nil || p.Settings == nil {
			p.Settings = map[string]string{}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SaveProfile inserts the profile when ID is 0, otherwise updates it (its kind never
// changes). It returns the ID.
func (s *Store) SaveProfile(p Profile) (int64, error) {
	settings, err := json.Marshal(p.Settings)
	if err != nil || p.Settings == nil {
		settings = []byte("{}")
	}
	ti := p.TestInterval
	if ti == "" {
		ti = "30m"
	}
	if p.ID == 0 {
		res, err := s.db.Exec(`INSERT INTO profiles (kind, name, port, username, password, enabled, auto_test, test_interval, settings) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			p.Kind, p.Name, p.Port, p.Username, p.Password, p.Enabled, p.AutoTest, ti, string(settings))
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	return p.ID, s.execOne(`UPDATE profiles SET name = ?, port = ?, username = ?, password = ?, enabled = ?, auto_test = ?, test_interval = ?, settings = ? WHERE id = ?`,
		p.Name, p.Port, p.Username, p.Password, p.Enabled, p.AutoTest, ti, string(settings), p.ID)
}

// ReplaceProfileNodes swaps a profile's check results for a new set in one transaction.
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
		if _, err := tx.Exec(`INSERT INTO profile_nodes (profile_id, config_id, rank, ok, latency_ms, note, error, response, tested_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			profileID, n.ConfigID, n.Rank, n.OK, n.LatencyMS, n.Label, n.Error, n.Response, formatDBTime(n.TestedAt)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListProfileNodes returns a profile's check results with their configs: the
// candidates best first, then (unless candidatesOnly) the failed ones. Results whose
// config is gone are left out.
func (s *Store) ListProfileNodes(profileID int64, candidatesOnly bool) ([]ProfileNode, []*model.ProxyConfig, error) {
	rows, err := s.db.Query(`
		SELECT pn.config_id, pn.rank, pn.ok, pn.latency_ms, pn.note, pn.error, pn.response, pn.tested_at
		FROM profile_nodes pn WHERE pn.profile_id = ? AND (pn.ok = 1 OR ? = 0)
		ORDER BY pn.ok DESC, pn.rank, pn.note`, profileID, candidatesOnly)
	if err != nil {
		return nil, nil, err
	}
	var nodes []ProfileNode
	var ids []string
	for rows.Next() {
		var n ProfileNode
		var tested string
		if err := rows.Scan(&n.ConfigID, &n.Rank, &n.OK, &n.LatencyMS, &n.Label, &n.Error, &n.Response, &tested); err != nil {
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
	keptNodes := nodes[:0]
	ordered := make([]*model.ProxyConfig, 0, len(nodes))
	for _, n := range nodes {
		if c := byID[n.ConfigID]; c != nil {
			keptNodes = append(keptNodes, n)
			ordered = append(ordered, c)
		}
	}
	return keptNodes, ordered, nil
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
