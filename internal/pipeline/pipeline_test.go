package pipeline

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/AmirAM03/velox/internal/config"
	"github.com/AmirAM03/velox/internal/engine"
	"github.com/AmirAM03/velox/internal/model"
)

type mockEngine struct {
	dialFunc func(ctx context.Context, cfg *model.ProxyConfig, network, addr string) (net.Conn, error)
	tlsFunc  func(ctx context.Context, cfg *model.ProxyConfig) error
}

func (m *mockEngine) Dial(ctx context.Context, cfg *model.ProxyConfig, network, addr string) (net.Conn, error) {
	if m.dialFunc != nil {
		return m.dialFunc(ctx, cfg, network, addr)
	}
	return (&net.Dialer{}).DialContext(ctx, network, addr)
}

func (m *mockEngine) TestTLSHandshake(ctx context.Context, cfg *model.ProxyConfig) error {
	if m.tlsFunc != nil {
		return m.tlsFunc(ctx, cfg)
	}
	return nil
}

func (m *mockEngine) Close() error {
	return nil
}

var _ engine.Engine = (*mockEngine)(nil)

func TestPipeline_Stage0_DNSTCP(t *testing.T) {
	// Start a real TCP listener
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)

	cfg := config.DefaultConfig().Pipeline
	p := New(&cfg, &mockEngine{}, nil, nil, nil)

	// Passing config
	goodCfg := &model.ProxyConfig{
		ID:      "good-tcp",
		Address: "127.0.0.1",
		Port:    port,
	}
	res := p.testDNSTCP(context.Background(), goodCfg)
	if !res.Success {
		t.Errorf("expected success for listening port, got error: %s", res.Error)
	}

	// Failing config (unreachable port)
	badCfg := &model.ProxyConfig{
		ID:      "bad-tcp",
		Address: "127.0.0.1",
		Port:    1, // privileged port, unlikely to have listener
	}
	res = p.testDNSTCP(context.Background(), badCfg)
	if res.Success {
		t.Errorf("expected failure for unreachable port, got success")
	}
}

func TestPipeline_Stage1_TLS(t *testing.T) {
	// Test non-TLS (should pass immediately)
	cfg := config.DefaultConfig().Pipeline
	p := New(&cfg, &mockEngine{}, nil, nil, nil)

	noTLSCfg := &model.ProxyConfig{
		ID:       "no-tls",
		Security: model.SecurityNone,
	}
	res := p.testTLS(context.Background(), noTLSCfg)
	if !res.Success {
		t.Errorf("expected success for SecurityNone, got error: %s", res.Error)
	}

	// Test REALITY delegates to engine.TestTLSHandshake
	mockEng := &mockEngine{
		tlsFunc: func(ctx context.Context, cfg *model.ProxyConfig) error {
			if cfg.ID == "fail-reality" {
				return fmt.Errorf("handshake failed")
			}
			return nil
		},
	}
	pReality := New(&cfg, mockEng, nil, nil, nil)

	passRealityCfg := &model.ProxyConfig{
		ID:       "pass-reality",
		Security: model.SecurityREALITY,
	}
	res = pReality.testTLS(context.Background(), passRealityCfg)
	if !res.Success {
		t.Errorf("expected success for pass-reality, got error: %s", res.Error)
	}

	failRealityCfg := &model.ProxyConfig{
		ID:       "fail-reality",
		Security: model.SecurityREALITY,
	}
	res = pReality.testTLS(context.Background(), failRealityCfg)
	if res.Success {
		t.Errorf("expected failure for fail-reality, got success")
	}
}

func TestPipeline_Stage2_Proxy(t *testing.T) {
	// Start mock HTTP server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent) // 204
	}))
	defer srv.Close()

	cfg := config.DefaultConfig().Pipeline
	mockEng := &mockEngine{
		dialFunc: func(ctx context.Context, cfg *model.ProxyConfig, network, addr string) (net.Conn, error) {
			// Directly dial the mock server
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}

	targets := []string{srv.URL}
	expect := []int{200, 204}
	p := New(&cfg, mockEng, targets, expect, nil)

	proxyCfg := &model.ProxyConfig{
		ID:      "proxy-test",
		Address: "127.0.0.1",
		Port:    80,
	}

	res := p.testProxy(context.Background(), proxyCfg)
	if !res.Success {
		t.Errorf("expected proxy test to succeed, got error: %s", res.Error)
	}
	if res.TargetURL != srv.URL {
		t.Errorf("expected target URL %s, got %s", srv.URL, res.TargetURL)
	}
}

func TestPipeline_Run_EndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)

	cfg := config.PipelineConfig{
		Stage0Workers: 5,
		Stage1Workers: 5,
		Stage2Workers: 5,
		Stage0Timeout: 2 * time.Second,
		Stage1Timeout: 2 * time.Second,
		Stage2Timeout: 2 * time.Second,
	}

	mockEng := &mockEngine{
		dialFunc: func(ctx context.Context, cfg *model.ProxyConfig, network, addr string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}

	p := New(&cfg, mockEng, []string{srv.URL}, []int{200}, nil)

	configs := []*model.ProxyConfig{
		{
			ID:       "cfg-pass",
			Address:  "127.0.0.1",
			Port:     port,
			Security: model.SecurityNone,
		},
		{
			ID:       "cfg-fail-tcp",
			Address:  "127.0.0.1",
			Port:     1,
			Security: model.SecurityNone,
		},
	}

	results := p.Run(context.Background(), configs)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	var passCount, failCount int
	for _, r := range results {
		if r.Failed {
			failCount++
		} else {
			passCount++
		}
	}

	if passCount != 1 || failCount != 1 {
		t.Errorf("expected 1 passed and 1 failed, got passed=%d, failed=%d", passCount, failCount)
	}
}

func TestPipeline_MethodologyChain_EarlyDisqualification(t *testing.T) {
	httpRunCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpRunCount++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)

	cfg := config.PipelineConfig{
		Stage0Workers: 2,
		Stage1Workers: 2,
		Stage2Workers: 2,
		Stage0Timeout: 1 * time.Second,
		Stage1Timeout: 1 * time.Second,
		Stage2Timeout: 1 * time.Second,
	}

	mockEng := &mockEngine{
		dialFunc: func(ctx context.Context, cfg *model.ProxyConfig, network, addr string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}

	p := New(&cfg, mockEng, []string{srv.URL}, []int{200}, nil)

	// Configure chain: Step 1 (TCP Ping, Necessary), Step 2 (HTTP Delay, Necessary)
	chain := model.BenchmarkMethodologyChain{
		ScoringMode: model.ScoringModePrimary,
		Steps: []model.TestStepConfig{
			{
				ID:        "step-1-ping",
				Type:      model.MethodologyTCPPing,
				Name:      "TCP Reachability",
				Enabled:   true,
				Necessary: true,
				Priority:  1,
				TimeoutMS: 500,
			},
			{
				ID:        "step-2-http",
				Type:      model.MethodologyHTTPDelay,
				Name:      "HTTP Delay",
				Enabled:   true,
				Necessary: true,
				Priority:  2,
				TimeoutMS: 1000,
				TargetURL: srv.URL,
			},
		},
	}
	p.SetMethodology(&chain)

	configs := []*model.ProxyConfig{
		{
			ID:       "cfg-good",
			Address:  "127.0.0.1",
			Port:     port,
			Security: model.SecurityNone,
		},
		{
			ID:       "cfg-bad-ping",
			Address:  "127.0.0.1",
			Port:     1, // Fails TCP Ping
			Security: model.SecurityNone,
		},
	}

	results := p.Run(context.Background(), configs)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// cfg-good should have 2 test results (ping + http)
	var goodRes, badRes *Result
	for i := range results {
		if results[i].Config.ID == "cfg-good" {
			goodRes = &results[i]
		} else if results[i].Config.ID == "cfg-bad-ping" {
			badRes = &results[i]
		}
	}

	if goodRes == nil || goodRes.Failed {
		t.Fatalf("expected cfg-good to pass")
	}
	if len(goodRes.Results) != 2 {
		t.Errorf("expected cfg-good to have 2 results, got %d", len(goodRes.Results))
	}

	if badRes == nil || !badRes.Failed {
		t.Fatalf("expected cfg-bad-ping to be failed")
	}
	// cfg-bad-ping MUST have only 1 result because it failed Necessary Step 1 and was disqualified before HTTP delay!
	if len(badRes.Results) != 1 {
		t.Errorf("expected cfg-bad-ping to be disqualified after 1 result, got %d", len(badRes.Results))
	}
	if httpRunCount != 1 {
		t.Errorf("expected HTTP test to be hit only once (for cfg-good), but was hit %d times", httpRunCount)
	}
}
