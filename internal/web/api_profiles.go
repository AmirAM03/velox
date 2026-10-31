package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

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
		s.optimizer.Trigger("refresh")
		writeJSON(w, http.StatusOK, map[string]any{"id": id})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// API: enable or disable a source (POST {"id", "enabled"}).
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

// API: list profiles with live state (GET) or create/update one (POST; id 0 creates).
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
		var p storage.Profile
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if err := s.validateProfile(&p); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		id, err := s.store.SaveProfile(p)
		if err != nil {
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
		s.optimizer.Trigger("cycle") // find nodes for the new or changed target soon
		writeJSON(w, http.StatusOK, map[string]any{"id": id})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// API: delete a profile (POST {"id"}); its listener stops at once.
func (s *Server) handleProfileDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID int64 `json:"id"`
	}
	if !decodePost(w, r, &req) {
		return
	}
	if err := s.store.DeleteProfile(req.ID); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err := s.profiles.Sync(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// API: optimizer status (GET) or schedule settings (POST).
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

// API: run now (POST {"kind": "refresh"|"cycle"}).
func (s *Server) handleOptimizerRun(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind string `json:"kind"`
	}
	if !decodePost(w, r, &req) {
		return
	}
	if req.Kind != "refresh" && req.Kind != "cycle" {
		writeError(w, http.StatusBadRequest, `kind must be "refresh" or "cycle"`)
		return
	}
	s.optimizer.Trigger(req.Kind)
	writeJSON(w, http.StatusOK, map[string]any{"status": "queued", "kind": req.Kind})
}

// validateProfile normalizes and checks a profile before it is saved.
// Profiles listen on a public address, so credentials are mandatory.
func (s *Server) validateProfile(p *storage.Profile) error {
	p.Name = strings.TrimSpace(p.Name)
	p.TargetURL = strings.TrimSpace(p.TargetURL)
	p.Username = strings.TrimSpace(p.Username)
	if p.Name == "" {
		return fmt.Errorf("name is required")
	}
	if !strings.Contains(p.TargetURL, "://") {
		p.TargetURL = "https://" + p.TargetURL
	}
	if err := checkHTTPURL(p.TargetURL); err != nil {
		return fmt.Errorf("target: %w", err)
	}
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
