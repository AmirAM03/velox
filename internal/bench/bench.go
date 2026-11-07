// Package bench runs a profile type's test over many nodes in two steps: a fast,
// highly concurrent filter (TCP connect, then the type's probe through each node),
// then the type's real check, once per node, on the survivors. What a probe and a
// check do is up to the profile's Check; bench runs them through nodes, in
// parallel, within limits, and ranks the nodes that pass by the check's latency.
package bench

import (
	"context"
	"net"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/AmirAM03/velox/internal/model"
)

// Limits of one run. They bound the whole test, not just a single request.
const (
	TCPWorkers = 512
	TCPTimeout = 2 * time.Second

	ProbeWorkers = 128
	ProbeTimeout = 6 * time.Second
	ProbeBudget  = 4 * time.Minute // whole filter step for one profile

	// shortcut: a check can use an account (e.g. a Google session) from each node's IP, so a run
	// checks at most VerifyMax nodes, VerifyWorkers at a time, and stops once VerifyTarget passed;
	// raise them only if the account can take more traffic from many IPs.
	VerifyWorkers = 6
	VerifyTarget  = 30
	VerifyMax     = 150
	VerifyTimeout = 10 * time.Second // per request
	VerifyBudget  = 6 * time.Minute  // whole check step for one profile
)

// Check is what a profile type tests through each node. Every request it sends
// with the given client goes out through that node on a fresh connection.
type Check interface {
	// Probe is the cheap filter: one request that proves the node reaches the service.
	// It must not use an account, because it runs through every reachable node.
	Probe(ctx context.Context, c *http.Client) error
	// Verify is the real check: whether the service accepts the node, a label for it
	// (e.g. the country the service placed it in) and the latency used for ranking.
	Verify(ctx context.Context, c *http.Client) Verdict
}

// Verdict is the outcome of one real check.
type Verdict struct {
	OK       bool
	Label    string        // e.g. the country the service placed the node in; may be set when !OK
	Latency  time.Duration // response time of the deciding request
	Err      string        // why the node failed, when !OK
	Response string        // raw body of the deciding response, kept for display
}

// Result is one node's verdict.
type Result struct {
	Config *model.ProxyConfig
	Verdict
}

// Dialer opens a connection to addr through a node (the Xray engine in production).
type Dialer interface {
	Dial(ctx context.Context, cfg *model.ProxyConfig, network, addr string) (net.Conn, error)
}

// Probe is a node that passed the filter step.
type Probe struct {
	Config  *model.ProxyConfig
	Latency time.Duration
}

// Reachable returns the configs whose server accepts a TCP connection.
func Reachable(ctx context.Context, cfgs []*model.ProxyConfig) []*model.ProxyConfig {
	var mu sync.Mutex
	var out []*model.ProxyConfig
	forEach(ctx, cfgs, TCPWorkers, nil, func(c *model.ProxyConfig) {
		dctx, cancel := context.WithTimeout(ctx, TCPTimeout)
		defer cancel()
		conn, err := (&net.Dialer{}).DialContext(dctx, "tcp", net.JoinHostPort(c.Address, strconv.Itoa(c.Port)))
		if err != nil {
			return
		}
		conn.Close()
		mu.Lock()
		out = append(out, c)
		mu.Unlock()
	})
	return out
}

// Filter runs the check's probe through every config and keeps those that pass,
// fastest first. onPass (may be nil) sees each passing node as it arrives.
func Filter(ctx context.Context, d Dialer, check Check, cfgs []*model.ProxyConfig, onPass func(Probe)) []Probe {
	ctx, cancel := context.WithTimeout(ctx, ProbeBudget)
	defer cancel()
	var mu sync.Mutex
	var out []Probe
	forEach(ctx, cfgs, ProbeWorkers, nil, func(c *model.ProxyConfig) {
		start := time.Now()
		if err := check.Probe(ctx, Client(d, c, ProbeTimeout)); err != nil {
			return
		}
		p := Probe{Config: c, Latency: time.Since(start)}
		mu.Lock()
		out = append(out, p)
		if onPass != nil {
			onPass(p) // under mu: callers get passes one at a time
		}
		mu.Unlock()
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Latency < out[j].Latency })
	return out
}

// Verify runs the real check once through the configs in order, until VerifyTarget
// passed or VerifyMax were checked. Every result goes to onResult (may be nil) as
// soon as it is in; calls never overlap. All results come back sorted (see SortResults).
func Verify(ctx context.Context, d Dialer, check Check, cfgs []*model.ProxyConfig, onResult func(Result)) []Result {
	ctx, cancel := context.WithTimeout(ctx, VerifyBudget)
	defer cancel()
	var mu sync.Mutex
	var out []Result
	started, passed := 0, 0
	more := func() bool {
		mu.Lock()
		defer mu.Unlock()
		if passed >= VerifyTarget || started >= VerifyMax {
			return false
		}
		started++
		return true
	}
	forEach(ctx, cfgs, VerifyWorkers, more, func(c *model.ProxyConfig) {
		r := Result{Config: c, Verdict: check.Verify(ctx, Client(d, c, VerifyTimeout))}
		if ctx.Err() != nil {
			return // cut off by the budget: no verdict
		}
		mu.Lock()
		out = append(out, r)
		if r.OK {
			passed++
		}
		if onResult != nil {
			onResult(r)
		}
		mu.Unlock()
	})
	SortResults(out)
	return out
}

// SortResults puts passing nodes first, lowest latency first; the rest keep their order.
func SortResults(rs []Result) {
	sort.SliceStable(rs, func(i, j int) bool {
		if rs[i].OK != rs[j].OK {
			return rs[i].OK
		}
		return rs[i].OK && rs[i].Latency < rs[j].Latency
	})
}

// Client sends every request through the node on a fresh connection, without following redirects.
func Client(d Dialer, c *model.ProxyConfig, timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return d.Dial(ctx, c, network, addr)
			},
			DisableKeepAlives: true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// forEach runs fn over cfgs with at most workers in flight. It stops starting new
// work when ctx ends or more (may be nil) returns false, and waits for what runs.
func forEach(ctx context.Context, cfgs []*model.ProxyConfig, workers int, more func() bool, fn func(*model.ProxyConfig)) {
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	defer wg.Wait()
	for _, c := range cfgs {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		if more != nil && !more() {
			<-sem
			return
		}
		wg.Add(1)
		go func(c *model.ProxyConfig) {
			defer func() { <-sem; wg.Done() }()
			fn(c)
		}(c)
	}
}
