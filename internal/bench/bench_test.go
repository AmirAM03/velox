package bench

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AmirAM03/velox/internal/model"
)

// fakeDialer connects straight to the target, delaying or failing per node name.
type fakeDialer map[string]time.Duration

func (f fakeDialer) Dial(ctx context.Context, cfg *model.ProxyConfig, network, addr string) (net.Conn, error) {
	delay, ok := f[cfg.Name]
	if !ok {
		return nil, errors.New("dead node")
	}
	time.Sleep(delay)
	return (&net.Dialer{}).DialContext(ctx, network, addr)
}

// urlCheck probes and checks one URL; a 403 answer marks the node not OK, like a blocked location.
type urlCheck struct {
	url    string
	checks atomic.Int32
}

func (u *urlCheck) Probe(ctx context.Context, c *http.Client) error {
	resp, err := c.Get(u.url)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (u *urlCheck) Verify(ctx context.Context, c *http.Client) Verdict {
	u.checks.Add(1)
	start := time.Now()
	resp, err := c.Get(u.url)
	if err != nil {
		return Verdict{Err: err.Error()}
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		return Verdict{Label: "XX", Err: "blocked"}
	}
	return Verdict{OK: true, Label: "OK", Latency: time.Since(start)}
}

func TestFilterAndVerifyRankNodes(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer target.Close()
	check := &urlCheck{url: target.URL}

	fast := &model.ProxyConfig{ID: "fast", Name: "fast"}
	slow := &model.ProxyConfig{ID: "slow", Name: "slow"}
	dead := &model.ProxyConfig{ID: "dead", Name: "dead"}
	d := fakeDialer{"fast": 0, "slow": 120 * time.Millisecond}

	passes := 0
	probes := Filter(context.Background(), d, check, []*model.ProxyConfig{slow, dead, fast}, func(Probe) { passes++ })
	if len(probes) != 2 || probes[0].Config != fast || probes[1].Config != slow || passes != 2 {
		t.Fatalf("probes = %+v (passes %d), want fast then slow, dead filtered", probes, passes)
	}

	seen := 0
	rs := Verify(context.Background(), d, check, []*model.ProxyConfig{dead, slow, fast}, func(Result) { seen++ })
	if len(rs) != 3 || rs[0].Config != fast || rs[1].Config != slow || rs[2].Config != dead || rs[2].OK || rs[0].Label != "OK" {
		t.Fatalf("results = %+v, want fast, slow, then failed dead", rs)
	}
	if seen != 3 || check.checks.Load() != 3 {
		t.Errorf("onResult saw %d, checks %d; want each node once", seen, check.checks.Load())
	}
}

func TestVerifyLabelsRejectedNodes(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer target.Close()
	rs := Verify(context.Background(), fakeDialer{"n": 0}, &urlCheck{url: target.URL}, []*model.ProxyConfig{{ID: "n", Name: "n"}}, nil)
	if len(rs) != 1 || rs[0].OK || rs[0].Label != "XX" {
		t.Fatalf("results = %+v, want one labeled, not OK", rs)
	}
}

// The check may use an account from each node's IP: a run must stop at the caps.
func TestVerifyStopsAtCaps(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer ok.Close()
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer blocked.Close()
	d := fakeDialer{}
	var cfgs []*model.ProxyConfig
	for i := 0; i < VerifyMax*2; i++ {
		name := fmt.Sprint(i)
		d[name] = 0
		cfgs = append(cfgs, &model.ProxyConfig{ID: name, Name: name})
	}
	if rs := Verify(context.Background(), d, &urlCheck{url: ok.URL}, cfgs, nil); len(rs) < VerifyTarget || len(rs) >= VerifyTarget+VerifyWorkers {
		t.Errorf("all passing: checked %d nodes, want about %d", len(rs), VerifyTarget)
	}
	check := &urlCheck{url: blocked.URL}
	if Verify(context.Background(), d, check, cfgs, nil); check.checks.Load() != VerifyMax {
		t.Errorf("all blocked: checked %d nodes, want the cap %d", check.checks.Load(), VerifyMax)
	}
}

func TestReachable(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	closed, _ := net.Listen("tcp", "127.0.0.1:0")
	closedPort := closed.Addr().(*net.TCPAddr).Port
	closed.Close()

	up := &model.ProxyConfig{Address: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port}
	down := &model.ProxyConfig{Address: "127.0.0.1", Port: closedPort}
	v6 := &model.ProxyConfig{Address: "::1", Port: 1} // must be dialed as [::1]:1, not "::1:1"
	got := Reachable(context.Background(), []*model.ProxyConfig{down, up, v6})
	if len(got) != 1 || got[0] != up {
		t.Fatalf("Reachable = %v, want only the open port", got)
	}
}
