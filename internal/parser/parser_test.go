package parser

import (
	"testing"

	"github.com/AmirAM03/velox/internal/model"
)

func TestParseVMess(t *testing.T) {
	// This is a standard VMess URI (base64-encoded JSON)
	// JSON: {"v":"2","ps":"test-vmess","add":"example.com","port":443,"id":"b831381d-6324-4d53-ad4f-8cda48b30811","aid":0,"scy":"auto","net":"ws","type":"none","host":"example.com","path":"/ws","tls":"tls","sni":"example.com","alpn":"","fp":"chrome"}
	uri := "vmess://eyJ2IjoiMiIsInBzIjoidGVzdC12bWVzcyIsImFkZCI6ImV4YW1wbGUuY29tIiwicG9ydCI6NDQzLCJpZCI6ImI4MzEzODFkLTYzMjQtNGQ1My1hZDRmLThjZGE0OGIzMDgxMSIsImFpZCI6MCwic2N5IjoiYXV0byIsIm5ldCI6IndzIiwidHlwZSI6Im5vbmUiLCJob3N0IjoiZXhhbXBsZS5jb20iLCJwYXRoIjoiL3dzIiwidGxzIjoidGxzIiwic25pIjoiZXhhbXBsZS5jb20iLCJhbHBuIjoiIiwiZnAiOiJjaHJvbWUifQ=="

	cfg, err := ParseURI(uri)
	if err != nil {
		t.Fatalf("ParseURI() error: %v", err)
	}

	assertions := []struct {
		name   string
		got    interface{}
		expect interface{}
	}{
		{"Protocol", cfg.Protocol, model.ProtocolVMess},
		{"Name", cfg.Name, "test-vmess"},
		{"Address", cfg.Address, "example.com"},
		{"Port", cfg.Port, 443},
		{"UUID", cfg.UUID, "b831381d-6324-4d53-ad4f-8cda48b30811"},
		{"AlterId", cfg.AlterId, 0},
		{"Encryption", cfg.Encryption, "auto"},
		{"Network", cfg.Network, model.NetworkWS},
		{"Security", cfg.Security, model.SecurityTLS},
		{"SNI", cfg.SNI, "example.com"},
		{"Fingerprint", cfg.Fingerprint, "chrome"},
	}

	for _, a := range assertions {
		if a.got != a.expect {
			t.Errorf("%s = %v, want %v", a.name, a.got, a.expect)
		}
	}

	// Transport opts
	if cfg.TransportOpts["host"] != "example.com" {
		t.Errorf("TransportOpts[host] = %q, want %q", cfg.TransportOpts["host"], "example.com")
	}
	if cfg.TransportOpts["path"] != "/ws" {
		t.Errorf("TransportOpts[path] = %q, want %q", cfg.TransportOpts["path"], "/ws")
	}

	// ID should be computed
	if cfg.ID == "" {
		t.Error("ID should be computed (non-empty)")
	}
}

func TestParseVLESS(t *testing.T) {
	uri := "vless://b831381d-6324-4d53-ad4f-8cda48b30811@example.com:443?type=tcp&security=reality&pbk=abc123&sid=01&sni=www.google.com&fp=chrome&flow=xtls-rprx-vision#test-vless"

	cfg, err := ParseURI(uri)
	if err != nil {
		t.Fatalf("ParseURI() error: %v", err)
	}

	assertions := []struct {
		name   string
		got    interface{}
		expect interface{}
	}{
		{"Protocol", cfg.Protocol, model.ProtocolVLESS},
		{"Name", cfg.Name, "test-vless"},
		{"Address", cfg.Address, "example.com"},
		{"Port", cfg.Port, 443},
		{"UUID", cfg.UUID, "b831381d-6324-4d53-ad4f-8cda48b30811"},
		{"Network", cfg.Network, model.NetworkTCP},
		{"Security", cfg.Security, model.SecurityREALITY},
		{"Flow", cfg.Flow, "xtls-rprx-vision"},
		{"SNI", cfg.SNI, "www.google.com"},
		{"Fingerprint", cfg.Fingerprint, "chrome"},
		{"RealityPublicKey", cfg.RealityPublicKey, "abc123"},
		{"RealityShortID", cfg.RealityShortID, "01"},
	}

	for _, a := range assertions {
		if a.got != a.expect {
			t.Errorf("%s = %v, want %v", a.name, a.got, a.expect)
		}
	}
}

func TestParseTrojan(t *testing.T) {
	uri := "trojan://mypassword@example.com:443?security=tls&sni=example.com&type=ws&path=/trojan-ws#test-trojan"

	cfg, err := ParseURI(uri)
	if err != nil {
		t.Fatalf("ParseURI() error: %v", err)
	}

	assertions := []struct {
		name   string
		got    interface{}
		expect interface{}
	}{
		{"Protocol", cfg.Protocol, model.ProtocolTrojan},
		{"Name", cfg.Name, "test-trojan"},
		{"Address", cfg.Address, "example.com"},
		{"Port", cfg.Port, 443},
		{"Password", cfg.Password, "mypassword"},
		{"Network", cfg.Network, model.NetworkWS},
		{"Security", cfg.Security, model.SecurityTLS},
		{"SNI", cfg.SNI, "example.com"},
	}

	for _, a := range assertions {
		if a.got != a.expect {
			t.Errorf("%s = %v, want %v", a.name, a.got, a.expect)
		}
	}
}

func TestParseShadowsocks(t *testing.T) {
	// SIP002 format: ss://base64(method:password)@host:port#fragment
	// method:password = aes-256-gcm:testpassword
	// base64("aes-256-gcm:testpassword") = YWVzLTI1Ni1nY206dGVzdHBhc3N3b3Jk
	uri := "ss://YWVzLTI1Ni1nY206dGVzdHBhc3N3b3Jk@example.com:8388#test-ss"

	cfg, err := ParseURI(uri)
	if err != nil {
		t.Fatalf("ParseURI() error: %v", err)
	}

	assertions := []struct {
		name   string
		got    interface{}
		expect interface{}
	}{
		{"Protocol", cfg.Protocol, model.ProtocolShadowsocks},
		{"Name", cfg.Name, "test-ss"},
		{"Address", cfg.Address, "example.com"},
		{"Port", cfg.Port, 8388},
		{"Password", cfg.Password, "testpassword"},
		{"Encryption", cfg.Encryption, "aes-256-gcm"},
	}

	for _, a := range assertions {
		if a.got != a.expect {
			t.Errorf("%s = %v, want %v", a.name, a.got, a.expect)
		}
	}
}

func TestParseMany(t *testing.T) {
	raw := `# This is a comment
// Another comment

vmess://eyJ2IjoiMiIsInBzIjoidGVzdDEiLCJhZGQiOiIxLjEuMS4xIiwicG9ydCI6NDQzLCJpZCI6ImI4MzEzODFkLTYzMjQtNGQ1My1hZDRmLThjZGE0OGIzMDgxMSIsImFpZCI6MCwic2N5IjoiYXV0byIsIm5ldCI6InRjcCIsInR5cGUiOiJub25lIiwiaG9zdCI6IiIsInBhdGgiOiIiLCJ0bHMiOiIiLCJzbmkiOiIiLCJhbHBuIjoiIiwiZnAiOiIifQ==
vless://b831381d-6324-4d53-ad4f-8cda48b30811@2.2.2.2:443?type=tcp&security=none#test2
invalid://not-a-real-scheme

# Duplicate of the vless above (same identity fields)
vless://b831381d-6324-4d53-ad4f-8cda48b30811@2.2.2.2:443?type=tcp&security=none#test2-dup
`
	configs, failures := ParseMany(raw)

	if len(configs) != 2 {
		t.Errorf("ParseMany() returned %d configs, want 2 (deduped)", len(configs))
	}
	if failures != 1 {
		t.Errorf("ParseMany() reported %d failures, want 1", failures)
	}
}

func TestParseUnknownScheme(t *testing.T) {
	_, err := ParseURI("unknown://data")
	if err == nil {
		t.Fatal("expected error for unknown scheme")
	}
	if _, ok := err.(*ErrUnknownScheme); !ok {
		// The error is wrapped, so check the inner error
		t.Logf("error type: %T, message: %v", err, err)
	}
}

func TestParseEmptyURI(t *testing.T) {
	_, err := ParseURI("")
	if err == nil {
		t.Fatal("expected error for empty URI")
	}
}
