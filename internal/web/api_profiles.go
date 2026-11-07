package web

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/AmirAM03/velox/internal/checks"
	"github.com/AmirAM03/velox/internal/storage"
)

// API: list (GET) or add (POST {"url"}) subscription sources.
func (s *Server) handleSources(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		sources, err := s.store.ListSources()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, sources)
	case http.MethodPost:
		var req struct {
			URL string `json:"url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		u := strings.TrimSpace(req.URL)
		if err := checkHTTPURL(u); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		id, err := s.store.AddSource(u)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				writeError(w, http.StatusConflict, "this subscription is already added")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.optimizer.TriggerRefresh()
		writeJSON(w, http.StatusOK, map[string]any{"id": id})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// API: enable or disable a source (POST {"id", "enabled"}).
// handleSourcePaste imports a pasted batch of config URIs or a base64 subscription body.
func (s *Server) handleSourcePaste(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20) // same cap as a fetched subscription
	var req struct {
		Content string `json:"content"`
	}
	if !decodePost(w, r, &req) {
		return
	}
	count, inserted, err := s.optimizer.Import(req.Content)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"configs": count, "new": inserted})
}

func (s *Server) handleSourceToggle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID      int64 `json:"id"`
		Enabled bool  `json:"enabled"`
	}
	if !decodePost(w, r, &req) {
		return
	}
	if err := s.store.SetSourceEnabled(req.ID, req.Enabled); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// API: delete a source (POST {"id"}). Its configs age out on later refreshes.
func (s *Server) handleSourceDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID int64 `json:"id"`
	}
	if !decodePost(w, r, &req) {
		return
	}
	if err := s.store.DeleteSource(req.ID); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// API: list profiles with live state (GET) or update one (POST). Profiles are the
// built-in types (checks.Kinds), created at startup; they cannot be added or deleted.
func (s *Server) handleProfiles(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		st, err := s.profiles.Status()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, st)
	case http.MethodPost:
		var req storage.Profile
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		p, ok := s.findProfile(w, req.ID)
		if !ok {
			return
		}
		if err := s.applyProfileEdit(&p, req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if _, err := s.store.SaveProfile(p); err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				writeError(w, http.StatusConflict, fmt.Sprintf("port %d is used by another profile", p.Port))
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := s.profiles.Sync(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if p.Enabled && p.AutoTest {
			s.optimizer.TriggerProfile(p.ID) // find nodes with the new settings now
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": p.ID})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ProfileResult is one config the profile's real check looked at, as listed on its card.
type ProfileResult struct {
	storage.ProfileNode
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Address  string `json:"address"`
	Port     int    `json:"port"`
	Network  string `json:"network"`
	Security string `json:"security"`
}

// API: every config the profile's last real check looked at (GET ?id=N), labeled:
// candidates first, best first, then the ones that failed and why.
func (s *Server) handleProfileResults(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if _, ok := s.findProfile(w, id); !ok {
		return
	}
	nodes, cfgs, err := s.store.ListProfileNodes(id, false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]ProfileResult, len(nodes))
	for i, n := range nodes {
		c := cfgs[i]
		out[i] = ProfileResult{ProfileNode: n, Name: c.DisplayName(), Protocol: string(c.Protocol), Address: c.Address,
			Port: c.Port, Network: string(c.Network), Security: string(c.Security)}
	}
	writeJSON(w, http.StatusOK, out)
}

// findProfile returns the stored profile with id, or writes a 404.
func (s *Server) findProfile(w http.ResponseWriter, id int64) (storage.Profile, bool) {
	profiles, err := s.store.ListProfiles()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return storage.Profile{}, false
	}
	for _, p := range profiles {
		if p.ID == id {
			return p, true
		}
	}
	writeError(w, http.StatusNotFound, "profile not found")
	return storage.Profile{}, false
}

// API: optimizer status (GET) or schedule settings (POST).
// handleProfileTest queues the full test flow for one profile.
func (s *Server) handleProfileTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID int64 `json:"id"`
	}
	if !decodePost(w, r, &req) {
		return
	}
	p, ok := s.findProfile(w, req.ID)
	if !ok {
		return
	}
	if !p.Enabled {
		writeError(w, http.StatusBadRequest, "enable the profile before testing it")
		return
	}
	if err := s.optimizer.TryTriggerProfile(p.ID); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "queued"})
}

// API: switch a profile's active node to one of its candidates (POST {id, config_id}).
func (s *Server) handleProfileActive(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID       int64  `json:"id"`
		ConfigID string `json:"config_id"`
	}
	if !decodePost(w, r, &req) {
		return
	}
	if _, ok := s.findProfile(w, req.ID); !ok {
		return
	}
	if err := s.profiles.SetActive(req.ID, req.ConfigID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleOptimizer(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.optimizer.Status())
	case http.MethodPost:
		var req OptimizerSettings
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if err := s.optimizer.Configure(req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, s.optimizer.Status())
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// API: fetch all subscriptions now (POST). Profile tests are started from their
// own cards, never here.
func (s *Server) handleOptimizerRun(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind string `json:"kind"`
	}
	if !decodePost(w, r, &req) {
		return
	}
	if req.Kind != "refresh" {
		writeError(w, http.StatusBadRequest, `kind must be "refresh"`)
		return
	}
	if err := s.optimizer.TryTriggerRefresh(); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "queued", "kind": "refresh"})
}

// applyProfileEdit copies the editable fields of req onto the stored profile p and
// checks the result. A secret setting left empty keeps its saved value, because the
// dashboard never receives secrets. Profiles listen on a public address, so
// credentials are mandatory, and an enabled profile needs settings its type accepts.
func (s *Server) applyProfileEdit(p *storage.Profile, req storage.Profile) error {
	kind, ok := checks.Get(p.Kind)
	if !ok {
		return fmt.Errorf("unknown profile type %q", p.Kind)
	}
	p.Port, p.Username, p.Password = req.Port, strings.TrimSpace(req.Username), req.Password
	p.Enabled, p.AutoTest = req.Enabled, req.AutoTest
	p.TestInterval = strings.TrimSpace(req.TestInterval)
	if p.TestInterval == "" {
		p.TestInterval = "30m"
	}
	if d, err := time.ParseDuration(p.TestInterval); err != nil || d < time.Minute {
		return fmt.Errorf("auto-test interval must be a duration of at least 1m (e.g. 30m)")
	}
	settings := maps.Clone(p.Settings) // keeps what sign-in stored, such as the account
	for _, f := range kind.Fields {
		v := strings.TrimSpace(req.Settings[f.Key])
		if v == "" && f.Secret {
			continue
		}
		settings[f.Key] = v
		if kind.Login != nil && f.Key == kind.Login.TokenField {
			delete(settings, checks.AccountKey) // a pasted session is not the signed-in account
		}
	}
	p.Settings = settings

	if p.Port < 1 || p.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	if p.Port == s.port {
		return fmt.Errorf("port %d is the dashboard port", p.Port)
	}
	if p.Username == "" || p.Password == "" {
		return fmt.Errorf("username and password are required (the port is reachable from the internet)")
	}
	if strings.Contains(p.Username, ":") || len(p.Username) > 255 || len(p.Password) > 255 {
		return fmt.Errorf("username must not contain ':' and both fields must be at most 255 characters")
	}
	if p.Enabled {
		if _, err := kind.New(p.Settings); err != nil {
			return fmt.Errorf("%s settings: %w", kind.Name, err)
		}
	}
	return nil
}

// seedProfiles creates the profile of every built-in type that has none yet:
// disabled, on the first free port from 20001, with a random password.
func (s *Server) seedProfiles() error {
	profiles, err := s.store.ListProfiles()
	if err != nil {
		return err
	}
	used := map[int]bool{s.port: true}
	have := map[string]storage.Profile{}
	for _, p := range profiles {
		used[p.Port] = true
		have[p.Kind] = p
	}
	for _, k := range checks.Kinds {
		if p, ok := have[k.ID]; ok {
			if p.Name != k.Name { // e.g. a migrated profile keeps its port, takes the type's name
				p.Name = k.Name
				if _, err := s.store.SaveProfile(p); err != nil {
					return err
				}
			}
			continue
		}
		port := 20001
		for used[port] {
			port++
		}
		used[port] = true
		pw := make([]byte, 12)
		if _, err := rand.Read(pw); err != nil {
			return err
		}
		if _, err := s.store.SaveProfile(storage.Profile{Kind: k.ID, Name: k.Name, Port: port, Username: "velox",
			Password: hex.EncodeToString(pw), AutoTest: true, Settings: map[string]string{}}); err != nil {
			return fmt.Errorf("create %s profile: %w", k.Name, err)
		}
	}
	return nil
}

func checkHTTPURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("must be an http:// or https:// URL")
	}
	return nil
}

// decodePost enforces POST and decodes the JSON body; it writes the error itself.
func decodePost(w http.ResponseWriter, r *http.Request, v any) bool {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return false
	}
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}
