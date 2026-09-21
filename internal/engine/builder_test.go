package engine

import (
	"encoding/json"
	"testing"

	"github.com/AmirAM03/velox/internal/model"
)

func TestBuildConfigJSON_VLESS_REALITY(t *testing.T) {
	cfg := &model.ProxyConfig{
		Protocol:         model.ProtocolVLESS,
		Address:          "server.com",
		Port:             443,
		UUID:             "11111111-2222-3333-4444-555555555555",
		Flow:             "xtls-rprx-vision",
		Network:          model.NetworkTCP,
		Security:         model.SecurityREALITY,
		SNI:              "sni.domain.com",
		Fingerprint:      "chrome",
		RealityPublicKey: "pubkey123",
		RealityShortID:   "shortid1",
	}

	data, err := BuildConfigJSON(cfg)
	if err != nil {
		t.Fatalf("BuildConfigJSON failed: %v", err)
	}

	var root map[string]interface{}
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}

	outbounds, ok := root["outbounds"].([]interface{})
	if !ok || len(outbounds) < 2 {
		t.Fatalf("expected at least 2 outbounds, got %v", outbounds)
	}

	proxyOutbound := outbounds[0].(map[string]interface{})
	if proxyOutbound["protocol"] != "vless" {
		t.Errorf("expected protocol vless, got %v", proxyOutbound["protocol"])
	}

	stream := proxyOutbound["streamSettings"].(map[string]interface{})
	if stream["security"] != "reality" {
		t.Errorf("expected reality security, got %v", stream["security"])
	}
	reality := stream["realitySettings"].(map[string]interface{})
	if reality["publicKey"] != "pubkey123" {
		t.Errorf("expected publicKey pubkey123, got %v", reality["publicKey"])
	}
	if reality["serverName"] != "sni.domain.com" {
		t.Errorf("expected serverName sni.domain.com, got %v", reality["serverName"])
	}
}

func TestBuildConfigJSON_VMess_WS_TLS(t *testing.T) {
	cfg := &model.ProxyConfig{
		Protocol: model.ProtocolVMess,
		Address:  "vmess.server.com",
		Port:     8443,
		UUID:     "22222222-2222-2222-2222-222222222222",
		AlterId:  0,
		Network:  model.NetworkWS,
		Security: model.SecurityTLS,
		SNI:      "vmess.sni.com",
		TransportOpts: map[string]string{
			"path": "/websocket",
			"host": "vmess.host.com",
		},
	}

	data, err := BuildConfigJSON(cfg)
	if err != nil {
		t.Fatalf("BuildConfigJSON failed: %v", err)
	}

	var root map[string]interface{}
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}

	outbounds := root["outbounds"].([]interface{})
	proxyOutbound := outbounds[0].(map[string]interface{})
	if proxyOutbound["protocol"] != "vmess" {
		t.Errorf("expected protocol vmess, got %v", proxyOutbound["protocol"])
	}

	stream := proxyOutbound["streamSettings"].(map[string]interface{})
	if stream["network"] != "ws" {
		t.Errorf("expected network ws, got %v", stream["network"])
	}
	wsSettings := stream["wsSettings"].(map[string]interface{})
	if wsSettings["path"] != "/websocket" {
		t.Errorf("expected path /websocket, got %v", wsSettings["path"])
	}
}

func TestBuildConfigJSON_Trojan(t *testing.T) {
	cfg := &model.ProxyConfig{
		Protocol: model.ProtocolTrojan,
		Address:  "trojan.server.com",
		Port:     443,
		Password: "trojan-password",
		Network:  model.NetworkTCP,
		Security: model.SecurityTLS,
		SNI:      "trojan.server.com",
	}

	data, err := BuildConfigJSON(cfg)
	if err != nil {
		t.Fatalf("BuildConfigJSON failed: %v", err)
	}

	var root map[string]interface{}
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}

	outbounds := root["outbounds"].([]interface{})
	proxyOutbound := outbounds[0].(map[string]interface{})
	if proxyOutbound["protocol"] != "trojan" {
		t.Errorf("expected protocol trojan, got %v", proxyOutbound["protocol"])
	}
}

func TestBuildConfigJSON_Shadowsocks(t *testing.T) {
	cfg := &model.ProxyConfig{
		Protocol:   model.ProtocolShadowsocks,
		Address:    "ss.server.com",
		Port:       8388,
		Password:   "ss-pass",
		Encryption: "aes-256-gcm",
		Network:    model.NetworkTCP,
		Security:   model.SecurityNone,
	}

	data, err := BuildConfigJSON(cfg)
	if err != nil {
		t.Fatalf("BuildConfigJSON failed: %v", err)
	}

	var root map[string]interface{}
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}

	outbounds := root["outbounds"].([]interface{})
	proxyOutbound := outbounds[0].(map[string]interface{})
	if proxyOutbound["protocol"] != "shadowsocks" {
		t.Errorf("expected protocol shadowsocks, got %v", proxyOutbound["protocol"])
	}
}
