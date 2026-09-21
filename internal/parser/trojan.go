package parser

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/AmirAM03/velox/internal/model"
)

// trojanParser handles trojan:// URIs.
// Format: trojan://password@host:port?params#fragment
type trojanParser struct{}

func init() {
	Register(&trojanParser{})
}

func (p *trojanParser) Scheme() string { return "trojan" }

func (p *trojanParser) Parse(uri string) (*model.ProxyConfig, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("url parse: %w", err)
	}

	// Extract password from userinfo
	password := u.User.Username()
	if password == "" {
		return nil, fmt.Errorf("missing password in userinfo")
	}

	// Host and port
	host := u.Hostname()
	if host == "" {
		return nil, fmt.Errorf("missing server address")
	}

	portStr := u.Port()
	if portStr == "" {
		portStr = "443" // Trojan defaults to 443
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return nil, fmt.Errorf("invalid port: %q", portStr)
	}

	q := u.Query()

	// Transport
	network := mapNetwork(q.Get("type"))

	// Security (Trojan defaults to TLS)
	security := model.SecurityTLS
	switch strings.ToLower(q.Get("security")) {
	case "none":
		security = model.SecurityNone
	case "reality":
		security = model.SecurityREALITY
	}

	// SNI (defaults to host if using TLS)
	sni := q.Get("sni")
	if sni == "" && security != model.SecurityNone {
		sni = host
	}

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
	if h := q.Get("host"); h != "" {
		opts["host"] = h
	}
	if path := q.Get("path"); path != "" {
		opts["path"] = path
	}
	if svcName := q.Get("serviceName"); svcName != "" {
		opts["serviceName"] = svcName
	}

	// Flow
	flow := q.Get("flow")

	// Fingerprint
	fp := q.Get("fp")

	// REALITY fields
	realityPubKey := q.Get("pbk")
	realityShortID := q.Get("sid")
	realitySpiderX := q.Get("spx")

	// Allow insecure
	allowInsecure := q.Get("allowInsecure") == "1" || strings.EqualFold(q.Get("allowInsecure"), "true")

	name := u.Fragment

	now := time.Now()
	config := &model.ProxyConfig{
		Name:             name,
		Protocol:         model.ProtocolTrojan,
		Address:          host,
		Port:             port,
		Password:         password,
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
