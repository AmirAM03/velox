package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/AmirAM03/velox/internal/bench"
	"github.com/AmirAM03/velox/internal/checks"
	"github.com/AmirAM03/velox/internal/storage"
)

// pendingLogin is a sign-in the admin started and the provider has not redirected back from yet.
type pendingLogin struct {
	profileID int64
	verifier  string
	redirect  string
	secret    string
	expires   time.Time
}

// logins holds sign-ins in flight, keyed by their random state value. One admin,
// a handful of entries: an in-memory map is enough; a restart only cancels them.
type logins struct {
	mu sync.Mutex
	m  map[string]pendingLogin
}

func (l *logins) add(state string, p pendingLogin) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.m == nil {
		l.m = map[string]pendingLogin{}
	}
	for k, v := range l.m {
		if time.Now().After(v.expires) {
			delete(l.m, k)
		}
	}
	l.m[state] = p
}

// take returns and forgets the sign-in for state; each one can be completed once.
func (l *logins) take(state string) (pendingLogin, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	p, ok := l.m[state]
	delete(l.m, state)
	return p, ok && time.Now().Before(p.expires)
}

// API: start signing a profile in to its service account (POST {"id", "client_secret"}).
// It returns the consent page URL; the browser goes there and the provider sends it
// back to the dashboard root with ?state=…&code=…, handled by handleLoginCallback.
func (s *Server) handleProfileLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID           int64  `json:"id"`
		ClientSecret string `json:"client_secret"`
	}
	if !decodePost(w, r, &req) {
		return
	}
	p, ok := s.findProfile(w, req.ID)
	if !ok {
		return
	}
	kind, _ := checks.Get(p.Kind)
	if kind.Login == nil {
		writeError(w, http.StatusBadRequest, "this profile type has no account sign-in")
		return
	}
	secret := strings.TrimSpace(req.ClientSecret)
	if secret == "" {
		secret = p.Settings[kind.Login.SecretField]
	}
	if secret == "" {
		writeError(w, http.StatusBadRequest, "enter the client secret first")
		return
	}
	state := make([]byte, 16)
	rand.Read(state)
	verifier, challenge := checks.PKCE()
	redirect := loopbackRedirect(r.Host)
	s.logins.add(hex.EncodeToString(state), pendingLogin{profileID: p.ID, verifier: verifier, redirect: redirect,
		secret: secret, expires: time.Now().Add(10 * time.Minute)})
	writeJSON(w, http.StatusOK, map[string]string{"url": kind.Login.URL(redirect, hex.EncodeToString(state), challenge)})
}

// loopbackRedirect is the dashboard origin as the admin's browser reaches it (through
// the SSH tunnel), on 127.0.0.1: desktop OAuth clients may redirect to any loopback port.
func loopbackRedirect(host string) string {
	if _, port, err := net.SplitHostPort(host); err == nil {
		return "http://127.0.0.1:" + port
	}
	return "http://127.0.0.1"
}

// handleLoginCallback finishes a sign-in: it trades the code for a refresh token,
// stores it on the profile and sends the browser back to the dashboard with the outcome.
func (s *Server) handleLoginCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	back := func(key, msg string) {
		http.Redirect(w, r, "/?"+url.Values{key: {msg}}.Encode(), http.StatusFound)
	}
	pend, ok := s.logins.take(q.Get("state"))
	if !ok {
		back("login_error", "this sign-in expired or was already used; start it again")
		return
	}
	if e := q.Get("error"); e != "" {
		back("login_error", "sign-in cancelled: "+e)
		return
	}
	profiles, err := s.store.ListProfiles()
	if err != nil {
		back("login_error", err.Error())
		return
	}
	var p storage.Profile
	for _, x := range profiles {
		if x.ID == pend.profileID {
			p = x
		}
	}
	kind, ok := checks.Get(p.Kind)
	if p.ID == 0 || !ok || kind.Login == nil {
		back("login_error", "profile not found")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	token, account, err := s.exchangeCode(ctx, p.ID, kind.Login, pend, q.Get("code"))
	if err != nil {
		s.logger.Warn("sign-in failed", "source", "profiles", "profile", p.Name, "error", err)
		back("login_error", err.Error())
		return
	}
	p.Settings[kind.Login.TokenField] = token
	p.Settings[kind.Login.SecretField] = pend.secret
	p.Settings[checks.AccountKey] = account
	if _, err := s.store.SaveProfile(p); err != nil {
		back("login_error", err.Error())
		return
	}
	s.logger.Info("profile signed in", "source", "profiles", "profile", p.Name, "account", account)
	if p.Enabled && p.AutoTest {
		s.optimizer.TriggerProfile(p.ID)
	}
	back("login", account)
}

// exchangeCode tries the server's own connection first, then the profile's nodes,
// because some VPS networks cannot reach the provider directly.
func (s *Server) exchangeCode(ctx context.Context, profileID int64, o *checks.OAuth, pend pendingLogin, code string) (string, string, error) {
	routes := []*http.Client{{Timeout: 20 * time.Second}}
	for _, c := range s.profiles.Nodes(profileID) {
		if len(routes) == 4 {
			break
		}
		routes = append(routes, bench.Client(s.profiles.engine, c, 20*time.Second))
	}
	var err error
	for _, c := range routes {
		var token, account string
		token, account, err = o.Exchange(ctx, c, pend.secret, code, pend.verifier, pend.redirect)
		var netErr *url.Error
		if err == nil || !errors.As(err, &netErr) || ctx.Err() != nil {
			return token, account, err // done, or the provider itself said no
		}
	}
	return "", "", err
}

// API: sign a profile out (POST {"id"}): its stored session is forgotten. Nodes it
// already serves keep working; the next test reports "not signed in".
func (s *Server) handleProfileLogout(w http.ResponseWriter, r *http.Request) {
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
	kind, _ := checks.Get(p.Kind)
	if kind.Login == nil {
		writeError(w, http.StatusBadRequest, "this profile type has no account sign-in")
		return
	}
	delete(p.Settings, kind.Login.TokenField)
	delete(p.Settings, checks.AccountKey)
	if _, err := s.store.SaveProfile(p); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}
