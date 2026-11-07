package checks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/AmirAM03/velox/internal/bench"
)

// Google decides whether the Antigravity (agy) API may be used from the IP a request
// arrives from, using its own geo-IP data, which often differs from public geo-IP
// sites. So the only reliable test is to ask Google, with agy's own session, through
// each node. Endpoints and behaviour verified in the agy-research project (2026-11-04).
const (
	// agy's OAuth client: the aud/azp claim of the id_token agy stores.
	agyClientID = "1071006060591-tmhssin2h21lcre235vtolojh4g403ep.apps.googleusercontent.com"
	// shortcut: approximated agy User-Agent; Google's location check is IP-based. Capture the real one if a check ever keys on it.
	agyUserAgent = "antigravity/1.4.12 windows/amd64"
	agyProject   = "aicode-consumers"

	googleTokenURL = "https://oauth2.googleapis.com/token"
	// 200 = location eligible; 403 LOCATION_NOT_ELIGIBLE carries Google's detected country.
	// Without a token it answers 401 before any location check, which makes it a free probe.
	aicodeFetchConfigURL = "https://aicode.googleapis.com/v1alpha:fetchConfig"
	// Returns Google's regionCode with 200 even when eligible.
	cloudcodeUserInfoURL = "https://daily-cloudcode-pa.googleapis.com/v1internal:fetchUserInfo"
)

var antigravityKind = Kind{
	ID:   "antigravity",
	Name: "Antigravity",
	Description: "Nodes Google accepts for the Antigravity (agy) API. Each node is asked through Google itself, " +
		"with your agy session, which country Google places it in and whether that country is eligible.",
	Fields: []Field{
		{Key: "client_secret", Label: "agy OAuth client secret", Secret: true,
			Help: "The GOCSPX-… secret embedded in the agy binary that pairs with agy's client id. Needed to sign in and to refresh the session."},
		{Key: "refresh_token", Label: "Session (refresh token)", Secret: true,
			Help: "Filled by Sign in with Google. Or paste the whole ~/.gemini/antigravity-cli/antigravity-oauth-token file of a logged-in agy, or only its refresh_token."},
	},
	// agy is a Google desktop OAuth client, so anyone may mint a session for their own
	// account with its client id (agy-research: gprobe login). Scopes are the ones agy's own token carries.
	Login: &OAuth{
		Provider: "Google", AuthURL: "https://accounts.google.com/o/oauth2/v2/auth", TokenURL: googleTokenURL, ClientID: agyClientID,
		Scopes: "openid https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/userinfo.profile " +
			"https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/aicode " +
			"https://www.googleapis.com/auth/experimentsandconfigs https://www.googleapis.com/auth/cclog",
		SecretField: "client_secret", TokenField: "refresh_token",
	},
	New: newAntigravity,
}

type antigravity struct {
	tokenURL, configURL, userInfoURL string
	refreshToken, clientSecret       string

	mu      sync.Mutex
	access  string
	expires time.Time
	fatal   error // Google rejected the session itself; every further round would fail too
}

func newAntigravity(s map[string]string) (bench.Check, error) {
	rt, err := parseRefreshToken(s["refresh_token"])
	if err != nil {
		return nil, err
	}
	secret := strings.TrimSpace(s["client_secret"])
	if !strings.HasPrefix(secret, "GOCSPX-") {
		return nil, fmt.Errorf("client secret must start with GOCSPX-")
	}
	return &antigravity{
		tokenURL: googleTokenURL, configURL: aicodeFetchConfigURL, userInfoURL: cloudcodeUserInfoURL,
		refreshToken: rt, clientSecret: secret,
	}, nil
}

// parseRefreshToken accepts agy's token file ({"token":{"refresh_token":…}}), a bare
// {"refresh_token":…} object, or the token itself.
func parseRefreshToken(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "{") {
		var f struct {
			RefreshToken string `json:"refresh_token"`
			Token        struct {
				RefreshToken string `json:"refresh_token"`
			} `json:"token"`
		}
		if err := json.Unmarshal([]byte(raw), &f); err != nil {
			return "", fmt.Errorf("session is not valid JSON: %w", err)
		}
		raw = f.Token.RefreshToken
		if raw == "" {
			raw = f.RefreshToken
		}
	}
	if raw == "" {
		return "", fmt.Errorf("not signed in: use Sign in with Google or paste an agy session")
	}
	return raw, nil
}

// Probe reaches Google's eligibility endpoint without a token: any HTTP answer over
// a verified TLS connection proves the node reaches Google, and uses no account.
func (a *antigravity) Probe(ctx context.Context, c *http.Client) error {
	_, _, err := a.do(ctx, c, http.MethodGet, a.configURL, "", nil, "")
	return err
}

// Verify asks Google, with the session, whether the node's location may use the
// API, and labels the node with the country Google places it in. The latency is the
// eligibility request's response time, which ranks the eligible nodes.
func (a *antigravity) Verify(ctx context.Context, c *http.Client) bench.Verdict {
	tok, err := a.token(ctx, c)
	if err != nil {
		return bench.Verdict{Err: err.Error()}
	}
	start := time.Now()
	status, body, err := a.do(ctx, c, http.MethodGet, a.configURL, tok, nil, "")
	lat := time.Since(start)
	if err != nil {
		return bench.Verdict{Err: err.Error()}
	}
	switch {
	case status == http.StatusOK:
		region, rbody := a.region(ctx, c, tok)
		return bench.Verdict{OK: true, Label: region, Latency: lat, Response: eligJSON(true, region, status, rbody)}
	case status == http.StatusForbidden && detectedLocation(body) != "":
		loc := detectedLocation(body)
		return bench.Verdict{Label: loc, Latency: lat, Err: "Google places this node in " + loc + ", not eligible", Response: eligJSON(false, loc, status, clipBody(body))}
	case status == http.StatusUnauthorized:
		a.mu.Lock()
		if a.access == tok {
			a.access = "" // expired early or revoked: refresh on the next check
		}
		a.mu.Unlock()
	}
	return bench.Verdict{Latency: lat, Err: fmt.Sprintf("fetchConfig: %d %s", status, googleMessage(body)), Response: eligJSON(false, "", status, clipBody(body))}
}

// region is Google's country code for an eligible node (fetchUserInfo), with the raw
// body for display; both "" when Google does not answer. This is the second request a
// passing node costs: Google returns the region here, not from fetchConfig.
func (a *antigravity) region(ctx context.Context, c *http.Client, tok string) (string, string) {
	status, body, err := a.do(ctx, c, http.MethodPost, a.userInfoURL, tok, []byte(`{"project":"`+agyProject+`"}`), "application/json")
	if err != nil {
		return "", ""
	}
	var r struct {
		RegionCode string `json:"regionCode"`
	}
	if status != http.StatusOK || json.Unmarshal(body, &r) != nil {
		return "", clipBody(body)
	}
	return r.RegionCode, clipBody(body)
}

// token returns a valid access token, refreshing it through this node when needed.
// Refreshes are serialized, so one run refreshes about once an hour.
func (a *antigravity) token(ctx context.Context, c *http.Client) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.fatal != nil {
		return "", a.fatal
	}
	if a.access != "" && time.Until(a.expires) > time.Minute {
		return a.access, nil
	}
	form := url.Values{
		"client_id": {agyClientID}, "client_secret": {a.clientSecret},
		"refresh_token": {a.refreshToken}, "grant_type": {"refresh_token"},
	}
	status, body, err := a.do(ctx, c, http.MethodPost, a.tokenURL, "", []byte(form.Encode()), "application/x-www-form-urlencoded")
	if err != nil {
		return "", fmt.Errorf("token refresh: %w", err)
	}
	if status == http.StatusBadRequest || status == http.StatusUnauthorized { // invalid_grant / invalid_client
		a.fatal = fmt.Errorf("Google rejected the agy session (%s): paste a fresh session or the right client secret", googleMessage(body))
		return "", a.fatal
	}
	var t struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if status != http.StatusOK || json.Unmarshal(body, &t) != nil || t.AccessToken == "" {
		return "", fmt.Errorf("token refresh: %d %s", status, googleMessage(body))
	}
	a.access, a.expires = t.AccessToken, time.Now().Add(time.Duration(t.ExpiresIn)*time.Second)
	return a.access, nil
}

// SessionError reports a session Google refused; the optimizer shows it on the profile.
func (a *antigravity) SessionError() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.fatal
}

func (a *antigravity) do(ctx context.Context, c *http.Client, method, u, tok string, body []byte, ctype string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("User-Agent", agyUserAgent)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, b, err
}

// googleError is the error shape of Google APIs and of the OAuth token endpoint.
type googleError struct {
	Error json.RawMessage `json:"error"`
	Desc  string          `json:"error_description"`
}

// detectedLocation is the country code in a 403 LOCATION_NOT_ELIGIBLE answer, else "".
func detectedLocation(body []byte) string {
	var e struct {
		Error struct {
			Details []struct {
				Reason   string            `json:"reason"`
				Metadata map[string]string `json:"metadata"`
			} `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) != nil {
		return ""
	}
	for _, d := range e.Error.Details {
		if d.Reason == "LOCATION_NOT_ELIGIBLE" {
			return d.Metadata["detected_location"]
		}
	}
	return ""
}

// eligJSON is what the check stores for the Response column: the two facts it decides
// on — eligibility and the detected region — together with Google's own answer nested
// under "detail". No single Google endpoint returns both for an eligible node, so this
// is assembled from fetchConfig (eligibility) and, when eligible, fetchUserInfo (region).
func eligJSON(eligible bool, region string, status int, detail string) string {
	out := map[string]any{"eligible": eligible, "region": region, "status": status}
	if detail != "" {
		if json.Valid([]byte(detail)) {
			out["detail"] = json.RawMessage(detail) // nest Google's JSON, not a quoted string
		} else {
			out["detail"] = detail
		}
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// clipBody keeps a response body for display, capped so one test cannot bloat the DB.
// shortcut: 64 KB per stored response; raise if a real eligibility body is ever larger.
func clipBody(body []byte) string {
	const max = 64 << 10
	if len(body) > max {
		return string(body[:max]) + "…"
	}
	return string(body)
}

// googleMessage is the human part of a Google error body, cut short.
func googleMessage(body []byte) string {
	var e googleError
	msg := ""
	if json.Unmarshal(body, &e) == nil {
		var obj struct {
			Message string `json:"message"`
		}
		var s string
		switch {
		case e.Desc != "":
			msg = e.Desc
		case json.Unmarshal(e.Error, &obj) == nil && obj.Message != "":
			msg = obj.Message
		case json.Unmarshal(e.Error, &s) == nil:
			msg = s
		}
	}
	if msg == "" {
		msg = strings.TrimSpace(string(body))
	}
	if len(msg) > 160 {
		msg = msg[:160] + "…"
	}
	return msg
}
