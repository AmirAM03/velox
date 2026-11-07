package checks

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// AccountKey is the setting where a signed-in profile keeps the account it signed in as (display only).
const AccountKey = "account"

// OAuth describes how a profile type signs in to its service account: the standard
// authorization-code flow for desktop apps (loopback redirect + PKCE), the same one
// gcloud and rclone use. The refresh token it yields goes into the TokenField setting.
type OAuth struct {
	Provider    string `json:"provider"` // shown on the button, e.g. "Google"
	AuthURL     string `json:"-"`
	TokenURL    string `json:"-"`
	ClientID    string `json:"-"`
	Scopes      string `json:"-"`
	SecretField string `json:"secret_field"` // setting holding the client secret
	TokenField  string `json:"token_field"`  // setting the refresh token is stored in
}

// PKCE returns a random code verifier and its S256 challenge.
func PKCE() (verifier, challenge string) {
	b := make([]byte, 48)
	rand.Read(b)
	verifier = base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

// URL is the consent page to send the admin's browser to. prompt=consent with
// offline access makes the provider return a refresh token every time.
func (o *OAuth) URL(redirect, state, challenge string) string {
	return o.AuthURL + "?" + url.Values{
		"client_id": {o.ClientID}, "redirect_uri": {redirect}, "response_type": {"code"},
		"scope": {o.Scopes}, "access_type": {"offline"}, "prompt": {"consent"},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "state": {state},
	}.Encode()
}

// Exchange trades the code from the redirect for a refresh token and the account
// e-mail. Network failures come back as *url.Error, so callers can retry another route.
func (o *OAuth) Exchange(ctx context.Context, c *http.Client, secret, code, verifier, redirect string) (refreshToken, account string, err error) {
	form := url.Values{
		"client_id": {o.ClientID}, "client_secret": {secret}, "code": {code}, "code_verifier": {verifier},
		"grant_type": {"authorization_code"}, "redirect_uri": {redirect},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	var t struct {
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &t) != nil {
		return "", "", fmt.Errorf("%s refused the sign-in: %s", o.Provider, googleMessage(body))
	}
	if t.RefreshToken == "" {
		return "", "", fmt.Errorf("%s returned no refresh token; remove Velox's access in your account settings and sign in again", o.Provider)
	}
	return t.RefreshToken, idTokenEmail(t.IDToken), nil
}

// idTokenEmail reads the e-mail claim of an ID token for display. The token came
// straight from the provider over TLS, so its signature is not checked.
func idTokenEmail(tok string) string {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	var claims struct {
		Email string `json:"email"`
	}
	if err != nil || json.Unmarshal(payload, &claims) != nil {
		return ""
	}
	return claims.Email
}
