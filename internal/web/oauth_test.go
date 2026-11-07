package web

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/AmirAM03/velox/internal/checks"
)

// Full sign-in round trip against a fake Google token endpoint: start, consent
// redirect back to the dashboard root, code exchange, stored session, sign-out.
func TestServer_SignInWithGoogle(t *testing.T) {
	srv, store, dbHandler := setupTestServer(t)
	defer store.Close()
	defer dbHandler.Close()
	defer srv.Close()
	if err := srv.seedProfiles(); err != nil {
		t.Fatal(err)
	}
	ps, _ := store.ListProfiles()

	claims := base64.RawURLEncoding.EncodeToString([]byte(`{"email":"me@example.com"}`))
	google := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("code") != "c1" || r.Form.Get("code_verifier") == "" || r.Form.Get("client_secret") != "GOCSPX-x" ||
			r.Form.Get("redirect_uri") != "http://127.0.0.1:18090" || r.Form.Get("grant_type") != "authorization_code" {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error":"invalid_grant","error_description":"Bad Request"}`))
			return
		}
		w.Write([]byte(`{"access_token":"at","refresh_token":"rt-new","id_token":"h.` + claims + `.s"}`))
	}))
	defer google.Close()
	login := *checks.Kinds[0].Login
	login.TokenURL = google.URL
	saved := checks.Kinds[0].Login
	checks.Kinds[0].Login = &login
	defer func() { checks.Kinds[0].Login = saved }()

	// Start: the browser reaches the dashboard through a tunnel on port 18090.
	req := httptest.NewRequest(http.MethodPost, "/api/profiles/login", strings.NewReader(`{"id":1,"client_secret":"GOCSPX-x"}`))
	req.Host = "127.0.0.1:18090"
	w := httptest.NewRecorder()
	srv.handleProfileLogin(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("start sign-in: %d %s", w.Code, w.Body)
	}
	var start struct{ URL string }
	json.Unmarshal(w.Body.Bytes(), &start)
	u, _ := url.Parse(start.URL)
	q := u.Query()
	if q.Get("redirect_uri") != "http://127.0.0.1:18090" || q.Get("code_challenge_method") != "S256" ||
		q.Get("access_type") != "offline" || q.Get("state") == "" || !strings.Contains(q.Get("scope"), "/auth/aicode") {
		t.Fatalf("consent URL = %s", start.URL)
	}

	// Google redirects back to the dashboard root.
	callback := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		srv.handleLoginCallback(w, httptest.NewRequest(http.MethodGet, "/?state="+q.Get("state")+"&code=c1", nil))
		return w
	}
	if w := callback(); w.Code != http.StatusFound || w.Header().Get("Location") != "/?login=me%40example.com" {
		t.Fatalf("callback: %d %s", w.Code, w.Header().Get("Location"))
	}
	ps, _ = store.ListProfiles()
	if ps[0].Settings["refresh_token"] != "rt-new" || ps[0].Settings["client_secret"] != "GOCSPX-x" || ps[0].Settings[checks.AccountKey] != "me@example.com" {
		t.Fatalf("stored settings = %v", ps[0].Settings)
	}
	if st, _ := srv.profiles.Status(); st[0].Account != "me@example.com" {
		t.Errorf("status account = %q", st[0].Account)
	}
	if w := callback(); !strings.Contains(w.Header().Get("Location"), "login_error") {
		t.Errorf("a sign-in completed twice: %s", w.Header().Get("Location"))
	}

	// Sign out forgets the session and account, keeps the client secret.
	w = httptest.NewRecorder()
	srv.handleProfileLogout(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"id":1}`)))
	ps, _ = store.ListProfiles()
	if w.Code != http.StatusOK || ps[0].Settings["refresh_token"] != "" || ps[0].Settings[checks.AccountKey] != "" || ps[0].Settings["client_secret"] != "GOCSPX-x" {
		t.Fatalf("after sign-out: %d %v", w.Code, ps[0].Settings)
	}
}
