package config

import "testing"

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.DBPath() == "" || cfg.Proxy.ListenAddr == "" || cfg.Proxy.FailThreshold <= 0 {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}
