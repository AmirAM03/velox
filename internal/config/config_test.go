package config

import (
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg == nil {
		t.Fatal("expected non-nil default config")
	}

	if err := cfg.Validate(); err != nil {
		t.Errorf("default config should be valid, got: %v", err)
	}

	if cfg.DBPath() == "" {
		t.Error("expected non-empty DBPath")
	}
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(c *Config)
		wantErr bool
	}{
		{
			name:    "valid",
			modify:  func(c *Config) {},
			wantErr: false,
		},
		{
			name: "invalid stage0 workers",
			modify: func(c *Config) {
				c.Pipeline.Stage0Workers = 0
			},
			wantErr: true,
		},
		{
			name: "empty targets",
			modify: func(c *Config) {
				c.Targets.URLs = nil
			},
			wantErr: true,
		},
		{
			name: "invalid mixed port zero",
			modify: func(c *Config) {
				c.Proxy.MixedPort = 0
			},
			wantErr: true,
		},
		{
			name: "invalid mixed port too high",
			modify: func(c *Config) {
				c.Proxy.MixedPort = 70000
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			tt.modify(cfg)
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
