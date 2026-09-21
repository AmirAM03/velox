package parser

import (
	"fmt"
	"strings"
	"time"

	"github.com/AmirAM03/velox/internal/model"
)

// hysteria2Parser handles hysteria2:// and hy2:// URIs.
// Format: hysteria2://auth@host:port?sni=...&insecure=1&obfs=salamander&obfs-password=...#fragment
type hysteria2Parser struct {
	scheme string
}

func init() {
	Register(&hysteria2Parser{scheme: "hysteria2"})
	Register(&hysteria2Parser{scheme: "hy2"})
}

func (p *hysteria2Parser) Scheme() string { return p.scheme }

func (p *hysteria2Parser) Parse(uri string) (*model.ProxyConfig, error) {
	parsed, err := SplitProxyURI(uri)
	if err != nil {
		return nil, fmt.Errorf("split hysteria2 URI: %w", err)
	}

	if parsed.Host == "" {
		return nil, fmt.Errorf("missing server address")
	}

	port := parsed.Port
	if port <= 0 {
		port = 443
	}
	if port > 65535 {
		return nil, fmt.Errorf("invalid port: %d", port)
	}

	q := parsed.Query

	// Security is always TLS in Hysteria2
	security := model.SecurityTLS

	// Insecure cert verification
	allowInsecure := q.Get("insecure") == "1" ||
		strings.EqualFold(q.Get("insecure"), "true") ||
		q.Get("allowInsecure") == "1" ||
		strings.EqualFold(q.Get("allowInsecure"), "true")

	// SNI defaults to host if not specified
	sni := q.Get("sni")
	if sni == "" {
		sni = parsed.Host
	}

	// ALPN: defaults to h3 if unspecified
	var alpn []string
	if alpnStr := q.Get("alpn"); alpnStr != "" {
		for _, a := range strings.Split(alpnStr, ",") {
			if a = strings.TrimSpace(a); a != "" {
				alpn = append(alpn, a)
			}
		}
	}
	if len(alpn) == 0 {
		alpn = []string{"h3"}
	}

	// Obfuscation
	obfs := q.Get("obfs")
	obfsPass := q.Get("obfs-password")
	if obfsPass == "" {
		obfsPass = q.Get("obfs_password")
	}

	// Bandwidth limits
	up := q.Get("up")
	down := q.Get("down")

	// Transport options
	opts := make(map[string]string)
	if mport := q.Get("mport"); mport != "" {
		opts["mport"] = mport
	}
	if pinSHA256 := q.Get("pinSHA256"); pinSHA256 != "" {
		opts["pinSHA256"] = pinSHA256
	}

	now := time.Now()
	config := &model.ProxyConfig{
		Name:              parsed.Fragment,
		Protocol:          model.ProtocolHysteria2,
		Address:           parsed.Host,
		Port:              port,
		Password:          parsed.UserInfo,
		Hysteria2Auth:     parsed.UserInfo,
		Hysteria2Obfs:     obfs,
		Hysteria2ObfsPass: obfsPass,
		Hysteria2Up:       up,
		Hysteria2Down:     down,
		Network:           model.NetworkQUIC,
		TransportOpts:     opts,
		Security:          security,
		SNI:               sni,
		ALPN:              alpn,
		AllowInsecure:     allowInsecure,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	return config, nil
}
