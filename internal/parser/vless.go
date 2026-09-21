package parser

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/AmirAM03/velox/internal/model"
)

// vlessParser handles vless:// URIs.
// VLESS URIs follow the standard URI format:
// vless://uuid@host:port?params#fragment
type vlessParser struct{}

func init() {
	Register(&vlessParser{})
}

func (p *vlessParser) Scheme() string { return "vless" }

func (p *vlessParser) Parse(uri string) (*model.ProxyConfig, error) {
	// Parse as standard URL (vless://uuid@host:port?params#fragment)
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("url parse: %w", err)
	}

	// Extract UUID from userinfo
	uuid := u.User.Username()
	if uuid == "" {
		return nil, fmt.Errorf("missing UUID in userinfo")
	}

	// Extract host and port
	host := u.Hostname()
	if host == "" {
		return nil, fmt.Errorf("missing server address")
	}

	portStr := u.Port()
	if portStr == "" {
		return nil, fmt.Errorf("missing port")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return nil, fmt.Errorf("invalid port: %q", portStr)
	}

	// Query parameters
	q := u.Query()

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

	// ALPN
	var alpn []string
	if alpnStr := q.Get("alpn"); alpnStr != "" {
		alpn = strings.Split(alpnStr, ",")
		for i := range alpn {
			alpn[i] = strings.TrimSpace(alpn[i])
		}
	}

	// Transport options
	opts := make(map[string]string)
	if host := q.Get("host"); host != "" {
		opts["host"] = host
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

	// Fragment (remark / display name)
	name := u.Fragment

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
		RealityPublicKey: realityPubKey,
		RealityShortID:   realityShortID,
		RealitySpiderX:   realitySpiderX,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	return config, nil
}
