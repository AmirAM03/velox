package model

import (
	"testing"
)

func TestProxyConfigHash(t *testing.T) {
	cfg1 := &ProxyConfig{
		Protocol:   ProtocolVLESS,
		Address:    "example.com",
		Port:       443,
		UUID:       "test-uuid",
		Encryption: "none",
		Network:    NetworkTCP,
		Security:   SecurityTLS,
	}

	cfg2 := &ProxyConfig{
		Protocol:   ProtocolVLESS,
		Address:    "example.com",
		Port:       443,
		UUID:       "test-uuid",
		Encryption: "none",
		Network:    NetworkTCP,
		Security:   SecurityTLS,
		Name:       "different-name", // Name should NOT affect hash
	}

	if cfg1.Hash() != cfg2.Hash() {
		t.Error("configs with same identity fields but different names should have same hash")
	}

	cfg3 := &ProxyConfig{
		Protocol:   ProtocolVLESS,
		Address:    "other.com", // Different address
		Port:       443,
		UUID:       "test-uuid",
		Encryption: "none",
		Network:    NetworkTCP,
		Security:   SecurityTLS,
	}

	if cfg1.Hash() == cfg3.Hash() {
		t.Error("configs with different addresses should have different hashes")
	}
}

func TestProxyConfigComputeID(t *testing.T) {
	cfg := &ProxyConfig{
		Protocol: ProtocolVMess,
		Address:  "1.2.3.4",
		Port:     443,
		UUID:     "some-uuid",
	}

	cfg.ComputeID()
	if cfg.ID == "" {
		t.Error("ComputeID should set a non-empty ID")
	}
	if len(cfg.ID) != 32 {
		t.Errorf("ID length = %d, want 32 (hex of 16 bytes)", len(cfg.ID))
	}
}

func TestProxyConfigDisplayName(t *testing.T) {
	cfg := &ProxyConfig{
		Name:     "My Server",
		Protocol: ProtocolVLESS,
		Address:  "example.com",
		Port:     443,
	}

	if cfg.DisplayName() != "My Server" {
		t.Errorf("DisplayName() = %q, want %q", cfg.DisplayName(), "My Server")
	}

	cfg.Name = ""
	expected := "vless://example.com:443"
	if cfg.DisplayName() != expected {
		t.Errorf("DisplayName() = %q, want %q", cfg.DisplayName(), expected)
	}
}

func TestHashCaseInsensitive(t *testing.T) {
	cfg1 := &ProxyConfig{
		Protocol: ProtocolVLESS,
		Address:  "Example.COM",
		Port:     443,
		UUID:     "B831381D-6324-4D53-AD4F-8CDA48B30811",
		SNI:      "MyDomain.COM",
	}
	cfg2 := &ProxyConfig{
		Protocol: ProtocolVLESS,
		Address:  "example.com",
		Port:     443,
		UUID:     "b831381d-6324-4d53-ad4f-8cda48b30811",
		SNI:      "mydomain.com",
	}

	if cfg1.Hash() != cfg2.Hash() {
		t.Error("address, UUID, and SNI hash should be case-insensitive")
	}
}

func TestHashPathNormalization(t *testing.T) {
	cfg1 := &ProxyConfig{
		Protocol:      ProtocolVLESS,
		Address:       "example.com",
		Port:          443,
		UUID:          "test-uuid",
		Network:       NetworkWS,
		TransportOpts: map[string]string{"path": "/"},
	}
	cfg2 := &ProxyConfig{
		Protocol:      ProtocolVLESS,
		Address:       "example.com",
		Port:          443,
		UUID:          "test-uuid",
		Network:       NetworkWS,
		TransportOpts: map[string]string{"path": ""},
	}

	if cfg1.Hash() != cfg2.Hash() {
		t.Error("empty path and root path '/' should normalize to the same hash")
	}

	// Trailing slash normalization
	cfg3 := &ProxyConfig{
		Protocol:      ProtocolVLESS,
		Address:       "example.com",
		Port:          443,
		UUID:          "test-uuid",
		Network:       NetworkWS,
		TransportOpts: map[string]string{"path": "/ws/"},
	}
	cfg4 := &ProxyConfig{
		Protocol:      ProtocolVLESS,
		Address:       "example.com",
		Port:          443,
		UUID:          "test-uuid",
		Network:       NetworkWS,
		TransportOpts: map[string]string{"path": "/ws"},
	}

	if cfg3.Hash() != cfg4.Hash() {
		t.Error("path '/ws/' and '/ws' should normalize to the same hash")
	}

	// Different path should NOT match
	cfg5 := &ProxyConfig{
		Protocol:      ProtocolVLESS,
		Address:       "example.com",
		Port:          443,
		UUID:          "test-uuid",
		Network:       NetworkWS,
		TransportOpts: map[string]string{"path": "/other"},
	}

	if cfg1.Hash() == cfg5.Hash() {
		t.Error("different paths should produce different hashes")
	}
}

func TestHashIPv6Brackets(t *testing.T) {
	cfg1 := &ProxyConfig{
		Protocol: ProtocolTrojan,
		Address:  "[2001:db8::1]",
		Port:     443,
		Password: "pass",
	}
	cfg2 := &ProxyConfig{
		Protocol: ProtocolTrojan,
		Address:  "2001:db8::1",
		Port:     443,
		Password: "pass",
	}

	if cfg1.Hash() != cfg2.Hash() {
		t.Error("IPv6 address with and without brackets should normalize to the same hash")
	}
}

