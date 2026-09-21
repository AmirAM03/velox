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
	}
	cfg2 := &ProxyConfig{
		Protocol: ProtocolVLESS,
		Address:  "example.com",
		Port:     443,
	}

	if cfg1.Hash() != cfg2.Hash() {
		t.Error("address hash should be case-insensitive")
	}
}
