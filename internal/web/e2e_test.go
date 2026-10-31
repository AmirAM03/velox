package web

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/xtls/xray-core/core"

	"github.com/AmirAM03/velox/internal/model"
	"github.com/AmirAM03/velox/internal/parser"
	"github.com/AmirAM03/velox/internal/proxy"
	"github.com/AmirAM03/velox/internal/storage"
)

// TestE2E_ProfileThroughRealXray sends an authenticated HTTP request through a
// profile listener, the shared Xray engine and a real (local) Shadowsocks server.
func TestE2E_ProfileThroughRealXray(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "hello through velox")
	}))
	defer target.Close()

	// Local Shadowsocks server played by Xray itself.
	ssPort := freePort(t)
	serverJSON := fmt.Sprintf(`{
		"log": {"loglevel": "none"},
		"inbounds": [{"listen": "127.0.0.1", "port": %d, "protocol": "shadowsocks",
			"settings": {"method": "aes-128-gcm", "password": "e2e-secret", "network": "tcp"}}],
		"outbounds": [{"protocol": "freedom"}]
	}`, ssPort)
	cfg, err := core.LoadConfig("json", bytes.NewReader([]byte(serverJSON)))
	if err != nil {
		t.Fatalf("load server config: %v", err)
	}
	server, err := core.New(cfg)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	if err := server.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer server.Close()

	userinfo := base64.RawURLEncoding.EncodeToString([]byte("aes-128-gcm:e2e-secret"))
	node, err := parser.ParseURI(fmt.Sprintf("ss://%s@127.0.0.1:%d#local", userinfo, ssPort))
	if err != nil {
		t.Fatalf("parse ss uri: %v", err)
	}

	// Same wiring ProfileManager.Sync does, minus the database.
	pm := NewProfileManager(nil, "127.0.0.1", 3, slog.Default())
	defer pm.Close()
	profilePort := freePort(t)
	sel := proxy.NewSelector(nil, 0, 3, slog.Default())
	sel.SetPool([]*model.ProxyConfig{node})
	srv := proxy.NewServer("127.0.0.1", profilePort, "u", "p", pm.engine, sel, slog.Default())
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	pm.running[1] = &profileRuntime{server: srv, selector: sel}

	proxyURL, _ := url.Parse(fmt.Sprintf("http://u:p@127.0.0.1:%d", profilePort))
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	resp, err := client.Get(target.URL)
	if err != nil {
		t.Fatalf("request through profile: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "hello through velox" {
		t.Fatalf("got %d %q", resp.StatusCode, body)
	}
}

func freePort(t *testing.T) int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// TestE2E_OptimizerAssignsWorkingNode runs a real optimizer cycle: one live
// Shadowsocks node and one dead node; the profile must end up serving via the live one.
func TestE2E_OptimizerAssignsWorkingNode(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	}))
	defer target.Close()

	ssPort := freePort(t)
	cfg, err := core.LoadConfig("json", bytes.NewReader([]byte(fmt.Sprintf(`{
		"log": {"loglevel": "none"},
		"inbounds": [{"listen": "127.0.0.1", "port": %d, "protocol": "shadowsocks",
			"settings": {"method": "aes-128-gcm", "password": "e2e-secret", "network": "tcp"}}],
		"outbounds": [{"protocol": "freedom"}]}`, ssPort))))
	if err != nil {
		t.Fatal(err)
	}
	server, err := core.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	srv, store, dbHandler := setupTestServer(t)
	defer store.Close()
	defer dbHandler.Close()
	defer srv.Close()

	userinfo := base64.RawURLEncoding.EncodeToString([]byte("aes-128-gcm:e2e-secret"))
	live, _ := parser.ParseURI(fmt.Sprintf("ss://%s@127.0.0.1:%d#live", userinfo, ssPort))
	dead, _ := parser.ParseURI(fmt.Sprintf("ss://%s@127.0.0.1:%d#dead", userinfo, freePort(t)))
	if _, _, err := store.UpsertConfigs([]*model.ProxyConfig{live, dead}); err != nil {
		t.Fatal(err)
	}

	profilePort := freePort(t)
	id, err := store.SaveProfile(storage.Profile{Name: "local", TargetURL: target.URL, Port: profilePort, Username: "u", Password: "p", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.profiles.Sync(); err != nil {
		t.Fatal(err)
	}

	summary := srv.optimizer.cycle(context.Background())
	nodes, _, _ := store.ListProfileNodes(id)
	if len(nodes) != 1 || nodes[0].ConfigID != live.ID {
		t.Fatalf("profile nodes = %+v (summary %q), want only the live node", nodes, summary)
	}

	proxyURL, _ := url.Parse(fmt.Sprintf("http://u:p@127.0.0.1:%d", profilePort))
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	resp, err := client.Get(target.URL)
	if err != nil {
		t.Fatalf("request through profile after cycle: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
}
