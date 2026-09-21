package parser

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/AmirAM03/velox/internal/model"
)

// ssParser handles ss:// (Shadowsocks) URIs.
// Supports both SIP002 format and legacy format:
//   SIP002:  ss://base64(method:password)@host:port#fragment
//   SIP002:  ss://base64(method:password)@host:port?plugin=...#fragment
//   Legacy:  ss://base64(method:password@host:port)#fragment
type ssParser struct{}

func init() {
	Register(&ssParser{})
}

func (p *ssParser) Scheme() string { return "ss" }

func (p *ssParser) Parse(uri string) (*model.ProxyConfig, error) {
	// Strip scheme
	body := strings.TrimPrefix(uri, "ss://")

	// Extract fragment (remark)
	name := ""
	if idx := strings.LastIndex(body, "#"); idx != -1 {
		name, _ = url.PathUnescape(body[idx+1:])
		body = body[:idx]
	}

	var method, password, host string
	var port int
	var pluginOpts map[string]string

	// Try SIP002 format first: base64(method:password)@host:port
	if atIdx := strings.LastIndex(body, "@"); atIdx != -1 {
		userInfo := body[:atIdx]
		serverPart := body[atIdx+1:]

		// Decode userinfo
		decoded, err := base64Decode(userInfo)
		if err != nil {
			// Maybe userinfo is not encoded (plain method:password)
			decoded = []byte(userInfo)
		}

		parts := strings.SplitN(string(decoded), ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid userinfo format: expected method:password")
		}
		method = parts[0]
		password = parts[1]

		// Parse server part, might have query params
		serverURI := "ss://" + "x@" + serverPart // Re-wrap for url.Parse
		u, err := url.Parse(serverURI)
		if err != nil {
			return nil, fmt.Errorf("parse server: %w", err)
		}

		host = u.Hostname()
		portStr := u.Port()
		if portStr == "" {
			return nil, fmt.Errorf("missing port")
		}
		port, err = strconv.Atoi(portStr)
		if err != nil || port <= 0 || port > 65535 {
			return nil, fmt.Errorf("invalid port: %q", portStr)
		}

		// Parse plugin options from query
		if plugin := u.Query().Get("plugin"); plugin != "" {
			pluginOpts = parseSSPlugin(plugin)
		}
	} else {
		// Legacy format: base64(method:password@host:port)
		decoded, err := base64Decode(body)
		if err != nil {
			return nil, fmt.Errorf("legacy base64 decode: %w", err)
		}

		// Parse as method:password@host:port
		atIdx := strings.LastIndex(string(decoded), "@")
		if atIdx == -1 {
			return nil, fmt.Errorf("invalid legacy format: no @ separator")
		}

		userPart := string(decoded[:atIdx])
		serverPart := string(decoded[atIdx+1:])

		parts := strings.SplitN(userPart, ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid legacy userinfo: expected method:password")
		}
		method = parts[0]
		password = parts[1]

		// Parse host:port
		colonIdx := strings.LastIndex(serverPart, ":")
		if colonIdx == -1 {
			return nil, fmt.Errorf("invalid legacy server: no port")
		}
		host = serverPart[:colonIdx]
		var parseErr error
		port, parseErr = strconv.Atoi(serverPart[colonIdx+1:])
		if parseErr != nil || port <= 0 || port > 65535 {
			return nil, fmt.Errorf("invalid port in legacy format")
		}
	}

	if host == "" {
		return nil, fmt.Errorf("missing server address")
	}

	// Build transport opts from plugin
	opts := make(map[string]string)
	network := model.NetworkTCP
	if pluginOpts != nil {
		for k, v := range pluginOpts {
			opts[k] = v
		}
		// Detect obfs/v2ray plugin transport
		if plugin, ok := opts["plugin"]; ok {
			switch {
			case strings.Contains(plugin, "v2ray-plugin"):
				if mode, ok := opts["mode"]; ok && mode == "websocket" {
					network = model.NetworkWS
				}
			}
		}
	}

	now := time.Now()
	config := &model.ProxyConfig{
		Name:          name,
		Protocol:      model.ProtocolShadowsocks,
		Address:       host,
		Port:          port,
		Password:      password,
		Encryption:    method,
		Network:       network,
		TransportOpts: opts,
		Security:      model.SecurityNone,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	return config, nil
}

// base64Decode for SS: tries all variants.
func base64DecodeSS(s string) ([]byte, error) {
	// Pad if necessary
	padded := s
	if m := len(s) % 4; m != 0 {
		padded += strings.Repeat("=", 4-m)
	}

	if decoded, err := base64.StdEncoding.DecodeString(padded); err == nil {
		return decoded, nil
	}
	if decoded, err := base64.URLEncoding.DecodeString(padded); err == nil {
		return decoded, nil
	}
	if decoded, err := base64.RawStdEncoding.DecodeString(s); err == nil {
		return decoded, nil
	}
	if decoded, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return decoded, nil
	}
	return nil, fmt.Errorf("base64 decode failed")
}

// parseSSPlugin parses Shadowsocks plugin option string.
// Format: "plugin_name;opt1=val1;opt2=val2"
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
