package engine

import (
	"bytes"
	"context"
	"testing"

	"github.com/xtls/xray-core/core"
	_ "github.com/xtls/xray-core/main/distro/all"
	"github.com/AmirAM03/velox/internal/model"
)

func TestCoreLoadConfig_VLESS(t *testing.T) {
	cfg := &model.ProxyConfig{
		Protocol:    model.ProtocolVLESS,
		Address:     "82.24.203.20",
		Port:        2087,
		UUID:        "11111111-2222-3333-4444-555555555555",
		Network:     model.NetworkWS,
		TransportOpts: map[string]string{
			"host": "example.com",
			"path": "/ws",
		},
		Security:    model.SecurityTLS,
		SNI:         "example.com",
	}

	data, err := BuildConfigJSON(cfg)
	if err != nil {
		t.Fatalf("BuildConfigJSON failed: %v", err)
	}

	xrayCfg, err := core.LoadConfig("json", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("core.LoadConfig failed: %v", err)
	}

	inst, err := core.New(xrayCfg)
	if err != nil {
		t.Fatalf("core.New failed: %v", err)
	}

	if err := inst.Start(); err != nil {
		t.Fatalf("inst.Start failed: %v", err)
	}
	defer inst.Close()

	eng := NewXrayEngine(nil)
	defer eng.Close()

	if err := eng.TestTLSHandshake(context.Background(), cfg); err != nil {
		t.Fatalf("TestTLSHandshake failed: %v", err)
	}
}
