package checks

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// Real Google answers, as captured by agy-research.
const blockedHK = `{"error":{"code":403,"message":"Your location is not eligible for this service at this time. Detected location: HK.",
"status":"PERMISSION_DENIED","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"LOCATION_NOT_ELIGIBLE",
"domain":"aicode.googleapis.com","metadata":{"detected_location":"HK","error_number":"1008"}}]}}`

// fakeGoogle answers like Google for a node whose location is eligible unless blocked is set.
func fakeGoogle(t *testing.T, blocked bool, refreshes *atomic.Int32) *antigravity {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			refreshes.Add(1)
			r.ParseForm()
			if r.Form.Get("refresh_token") != "rt" || r.Form.Get("client_secret") != "GOCSPX-ok" || r.Form.Get("client_id") != agyClientID {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`))
				return
			}
			w.Write([]byte(`{"access_token":"at","expires_in":3599}`))
		case "/config":
			if r.Header.Get("Authorization") != "Bearer at" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if blocked {
				w.WriteHeader(http.StatusForbidden)
				w.Write([]byte(blockedHK))
				return
			}
			w.Write([]byte(`{}`))
		case "/userinfo":
			w.Write([]byte(`{"userSettings":{"telemetryEnabled":true},"regionCode":"DE"}`))
		}
	}))
	t.Cleanup(srv.Close)
	chk, err := newAntigravity(map[string]string{"refresh_token": `{"token":{"refresh_token":"rt"},"auth_method":"consumer"}`, "client_secret": "GOCSPX-ok"})
	if err != nil {
		t.Fatal(err)
	}
	a := chk.(*antigravity)
	a.tokenURL, a.configURL, a.userInfoURL = srv.URL+"/token", srv.URL+"/config", srv.URL+"/userinfo"
	return a
}

func TestAntigravityEligibleNodeGetsGoogleCountry(t *testing.T) {
	var refreshes atomic.Int32
	a := fakeGoogle(t, false, &refreshes)
	if err := a.Probe(context.Background(), http.DefaultClient); err != nil {
		t.Fatalf("probe: %v", err)
	}
	for i := 0; i < 3; i++ {
		v := a.Verify(context.Background(), http.DefaultClient)
		if !v.OK || v.Label != "DE" || !strings.Contains(v.Response, `"eligible":true`) || !strings.Contains(v.Response, `"region":"DE"`) {
			t.Fatalf("verdict %d = %+v, want eligible in DE with eligibility+region in Response", i, v)
		}
	}
	if refreshes.Load() != 1 {
		t.Errorf("token refreshed %d times, want once per run", refreshes.Load())
	}
}

func TestAntigravityBlockedNodeIsLabeledWithCountry(t *testing.T) {
	var refreshes atomic.Int32
	v := fakeGoogle(t, true, &refreshes).Verify(context.Background(), http.DefaultClient)
	if v.OK || v.Label != "HK" || !strings.Contains(v.Err, "not eligible") ||
		!strings.Contains(v.Response, `"eligible":false`) || !strings.Contains(v.Response, `"region":"HK"`) ||
		!strings.Contains(v.Response, "LOCATION_NOT_ELIGIBLE") {
		t.Fatalf("verdict = %+v; want not eligible, labeled HK, eligibility+region+Google's body in Response", v)
	}
}

func TestAntigravityRefusedSessionStopsAllChecks(t *testing.T) {
	var refreshes atomic.Int32
	a := fakeGoogle(t, false, &refreshes)
	a.refreshToken = "revoked"
	for i := 0; i < 3; i++ {
		if v := a.Verify(context.Background(), http.DefaultClient); v.OK {
			t.Fatal("check passed with a refused session")
		}
	}
	if err := a.SessionError(); err == nil || !strings.Contains(err.Error(), "expired or revoked") {
		t.Errorf("SessionError = %v, want Google's reason", err)
	}
	if refreshes.Load() != 1 {
		t.Errorf("refused session retried %d times, want once", refreshes.Load())
	}
}

func TestAntigravitySettings(t *testing.T) {
	for _, s := range []map[string]string{
		{"refresh_token": "", "client_secret": "GOCSPX-x"},
		{"refresh_token": "rt", "client_secret": "nope"},
		{"refresh_token": "{bad json", "client_secret": "GOCSPX-x"},
	} {
		if _, err := newAntigravity(s); err == nil {
			t.Errorf("settings %v accepted", s)
		}
	}
	if rt, _ := parseRefreshToken(" 1//abc "); rt != "1//abc" {
		t.Errorf("bare token = %q", rt)
	}
}
