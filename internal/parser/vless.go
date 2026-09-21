package parser

import (
	"fmt"
	"strings"
	"time"

	"github.com/AmirAM03/velox/internal/model"
)

// vlessParser handles vless:// URIs.
// Format: vless://uuid@host:port?params#fragment
type vlessParser struct{}

func init() {
	Register(&vlessParser{})
}

func (p *vlessParser) Scheme() string { return "vless" }

func (p *vlessParser) Parse(uri string) (*model.ProxyConfig, error) {
	parsed, err := SplitProxyURI(uri)
	if err != nil {
		return nil, fmt.Errorf("split vless URI: %w", err)
	}

	uuid := parsed.UserInfo
	if uuid == "" {
		return nil, fmt.Errorf("missing UUID in userinfo")
	}

	host := parsed.Host
	if host == "" {
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

	// Transport type
	network := mapNetwork(q.Get("type"))

	// Security / TLS
	security := model.SecurityNone
	switch strings.ToLower(q.Get("security")) {
	case "tls":
		security = model.SecurityTLS
	case "reality":
		security = model.SecurityREALITY
	}

	// Encryption
	encryption := q.Get("encryption")
	if encryption == "" {
		encryption = "none"
	}

	// Flow (XTLS)
	flow := q.Get("flow")

	// SNI
	sni := q.Get("sni")

	// Fingerprint
	fp := q.Get("fp")

	// Allow insecure
	allowInsecure := q.Get("insecure") == "1" ||
		strings.EqualFold(q.Get("insecure"), "true") ||
		q.Get("allowInsecure") == "1" ||
		strings.EqualFold(q.Get("allowInsecure"), "true")

	// ALPN
	var alpn []string
	if alpnStr := q.Get("alpn"); alpnStr != "" {
		for _, a := range strings.Split(alpnStr, ",") {
			if a = strings.TrimSpace(a); a != "" {
				alpn = append(alpn, a)
			}
		}
	}

	// Transport options
	opts := make(map[string]string)
	if h := q.Get("host"); h != "" {
		opts["host"] = h
	}
	if path := q.Get("path"); path != "" {
		opts["path"] = path
	}
	if svcName := q.Get("serviceName"); svcName != "" {
		opts["serviceName"] = svcName
	}
	if headerType := q.Get("headerType"); headerType != "" && headerType != "none" {
		opts["headerType"] = headerType
	}
	if mode := q.Get("mode"); mode != "" {
		opts["mode"] = mode
	}

	// REALITY fields
	realityPubKey := q.Get("pbk")
	realityShortID := q.Get("sid")
	realitySpiderX := q.Get("spx")

	name := parsed.Fragment

	now := time.Now()
	config := &model.ProxyConfig{
		Name:             name,
		Protocol:         model.ProtocolVLESS,
		Address:          host,
		Port:             port,
		UUID:             uuid,
		Encryption:       encryption,
		Flow:             flow,
		Network:          network,
		TransportOpts:    opts,
		Security:         security,
		SNI:              sni,
		ALPN:             alpn,
		Fingerprint:      fp,
		AllowInsecure:    allowInsecure,
		RealityPublicKey: realityPubKey,
		RealityShortID:   realityShortID,
		RealitySpiderX:   realitySpiderX,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	return config, nil
}
