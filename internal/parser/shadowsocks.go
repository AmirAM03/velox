package parser

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/AmirAM03/velox/internal/model"
)

// ssParser handles ss:// (Shadowsocks) URIs.
// Supports:
//   SIP002:  ss://base64(method:password)@host:port?params#fragment
//   Plain:   ss://method:password@host:port?params#fragment
//   Legacy:  ss://base64(method:password@host:port)#fragment
type ssParser struct{}

func init() {
	Register(&ssParser{})
}

func (p *ssParser) Scheme() string { return "ss" }

func (p *ssParser) Parse(uri string) (*model.ProxyConfig, error) {
	// First check if this is SIP002 / has userinfo and host
	parsed, err := SplitProxyURI(uri)
	if err == nil && parsed.Host != "" && parsed.Port > 0 {
		return p.parseSIP002(parsed, uri)
	}

	// Otherwise, handle legacy format or fallback
	return p.parseLegacy(uri)
}

func (p *ssParser) parseSIP002(parsed *ParsedProxyURI, rawURI string) (*model.ProxyConfig, error) {
	userInfo := parsed.UserInfo
	if unescaped, err := url.QueryUnescape(userInfo); err == nil {
		userInfo = unescaped
	}

	var method, password string

	// 1. Try decoding userinfo as base64
	decoded, err := base64Decode(userInfo)
	if err == nil && strings.Contains(string(decoded), ":") {
		parts := strings.SplitN(string(decoded), ":", 2)
		method = parts[0]
		password = parts[1]
	} else if strings.Contains(userInfo, ":") {
		// Plain method:password
		parts := strings.SplitN(userInfo, ":", 2)
		method = parts[0]
		password = parts[1]
	} else if userInfo != "" {
		// Single token: check query for method/encryption
		enc := parsed.Query.Get("encryption")
		if enc == "" {
			enc = parsed.Query.Get("method")
		}
		if enc == "" {
			enc = "none"
		}
		method = enc
		password = userInfo
	} else {
		return nil, fmt.Errorf("missing userinfo in shadowsocks URI")
	}

	// Transport options and plugins
	opts := make(map[string]string)
	network := model.NetworkTCP
	q := parsed.Query

	if plugin := q.Get("plugin"); plugin != "" {
		for k, v := range parseSSPlugin(plugin) {
			opts[k] = v
		}
	}
	if t := q.Get("type"); t != "" {
		network = mapNetwork(t)
	}
	if h := q.Get("host"); h != "" {
		opts["host"] = h
	}
	if path := q.Get("path"); path != "" {
		opts["path"] = path
	}

	// Detect v2ray-plugin websocket
	if plugin, ok := opts["plugin"]; ok {
		if strings.Contains(plugin, "v2ray-plugin") {
			if mode, ok := opts["mode"]; ok && mode == "websocket" {
				network = model.NetworkWS
			}
		}
	}

	security := model.SecurityNone
	if strings.EqualFold(q.Get("security"), "tls") {
		security = model.SecurityTLS
	}

	now := time.Now()
	return &model.ProxyConfig{
		Name:          parsed.Fragment,
		Protocol:      model.ProtocolShadowsocks,
		Address:       parsed.Host,
		Port:          parsed.Port,
		Password:      password,
		Encryption:    method,
		Network:       network,
		TransportOpts: opts,
		Security:      security,
		SNI:           q.Get("sni"),
		CreatedAt:     now,
		UpdatedAt:     now,
	}, nil
}

func (p *ssParser) parseLegacy(uri string) (*model.ProxyConfig, error) {
	body := strings.TrimPrefix(uri, "ss://")
	name := ""
	if idx := strings.LastIndex(body, "#"); idx != -1 {
		name = body[idx+1:]
		body = body[:idx]
		if unescaped, err := url.QueryUnescape(name); err == nil {
			name = unescaped
		}
	}

	decoded, err := base64Decode(body)
	if err != nil {
		return nil, fmt.Errorf("legacy base64 decode: %w", err)
	}

	decodedStr := strings.TrimSpace(string(decoded))

	// Check if this is a mislabeled VMess JSON
	if strings.HasPrefix(decodedStr, "{") && strings.Contains(decodedStr, `"add"`) {
		vmessP := &vmessParser{}
		cfg, err := vmessP.Parse("vmess://" + body)
		if err == nil {
			if cfg.Name == "" && name != "" {
				cfg.Name = name
			}
			return cfg, nil
		}
	}

	atIdx := strings.LastIndex(decodedStr, "@")
	if atIdx == -1 {
		return nil, fmt.Errorf("invalid legacy format: no @ separator")
	}

	userPart := decodedStr[:atIdx]
	serverPart := decodedStr[atIdx+1:]

	parts := strings.SplitN(userPart, ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid legacy userinfo: expected method:password")
	}
	method := parts[0]
	password := parts[1]

	colonIdx := strings.LastIndex(serverPart, ":")
	if colonIdx == -1 {
		return nil, fmt.Errorf("invalid legacy server: no port")
	}
	host := serverPart[:colonIdx]
	port, err := strconv.Atoi(serverPart[colonIdx+1:])
	if err != nil || port <= 0 || port > 65535 {
		return nil, fmt.Errorf("invalid port in legacy format: %s", serverPart[colonIdx+1:])
	}

	now := time.Now()
	return &model.ProxyConfig{
		Name:       name,
		Protocol:   model.ProtocolShadowsocks,
		Address:    host,
		Port:       port,
		Password:   password,
		Encryption: method,
		Network:    model.NetworkTCP,
		Security:   model.SecurityNone,
		CreatedAt:  now,
		UpdatedAt:  now,
	}, nil
}

// parseSSPlugin parses Shadowsocks plugin option string: "plugin_name;opt1=val1;opt2=val2"
func parseSSPlugin(s string) map[string]string {
	opts := make(map[string]string)
	parts := strings.Split(s, ";")
	if len(parts) > 0 {
		opts["plugin"] = parts[0]
	}
	for _, part := range parts[1:] {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) == 2 {
			opts[kv[0]] = kv[1]
		}
	}
	return opts
}
