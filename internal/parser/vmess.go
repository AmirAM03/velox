package parser

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/AmirAM03/velox/internal/model"
)

// vmessParser handles vmess:// URIs.
// VMess URIs are base64-encoded JSON objects.
type vmessParser struct{}

func init() {
	Register(&vmessParser{})
}

func (p *vmessParser) Scheme() string { return "vmess" }

// vmessJSON is the JSON structure inside a vmess:// URI.
type vmessJSON struct {
	V    interface{} `json:"v"`    // Version (string or int, usually "2")
	PS   string      `json:"ps"`   // Remark / display name
	Add  string      `json:"add"`  // Server address
	Port interface{} `json:"port"` // Server port (string or int)
	ID   string      `json:"id"`   // UUID
	Aid  interface{} `json:"aid"`  // Alter ID (string or int)
	Scy  string      `json:"scy"`  // Encryption method
	Net  string      `json:"net"`  // Transport network
	Type string      `json:"type"` // Header type (for KCP/TCP)
	Host string      `json:"host"` // HTTP host / WS host
	Path string      `json:"path"` // WS path / H2 path / gRPC serviceName
	TLS  string      `json:"tls"`  // TLS ("tls" or "")
	SNI  string      `json:"sni"`  // Server Name Indication
	ALPN string      `json:"alpn"` // ALPN (comma-separated)
	FP   string      `json:"fp"`   // Fingerprint
}

func (p *vmessParser) Parse(uri string) (*model.ProxyConfig, error) {
	// Strip scheme
	encoded := strings.TrimPrefix(uri, "vmess://")
	encoded = strings.TrimSpace(encoded)

	// Extract remark from fragment if present
	var remark string
	if idx := strings.LastIndex(encoded, "#"); idx != -1 {
		remark = encoded[idx+1:]
		encoded = encoded[:idx]
		if unescaped, err := url.QueryUnescape(remark); err == nil {
			remark = unescaped
		}
	}
	encoded = strings.TrimSpace(encoded)

	// Decode base64 (handle both standard and URL-safe, with and without padding)
	decoded, err := base64Decode(encoded)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}

	var v vmessJSON
	if err := json.Unmarshal(decoded, &v); err != nil {
		return nil, fmt.Errorf("json unmarshal: %w", err)
	}

	// Validate required fields
	if v.Add == "" {
		return nil, fmt.Errorf("missing server address")
	}
	if v.ID == "" {
		return nil, fmt.Errorf("missing UUID")
	}

	port, err := toInt(v.Port)
	if err != nil || port <= 0 || port > 65535 {
		return nil, fmt.Errorf("invalid port: %v", v.Port)
	}

	alterId, _ := toInt(v.Aid)

	// Map network string
	network := mapNetwork(v.Net)

	// Map security
	security := model.SecurityNone
	if strings.EqualFold(v.TLS, "tls") {
		security = model.SecurityTLS
	}

	// Build transport opts
	opts := make(map[string]string)
	if v.Host != "" {
		opts["host"] = v.Host
	}
	if v.Path != "" {
		opts["path"] = v.Path
	}
	if v.Type != "" && v.Type != "none" {
		opts["headerType"] = v.Type
	}

	// Encryption defaults
	encryption := v.Scy
	if encryption == "" {
		encryption = "auto"
	}

	// Parse ALPN
	var alpn []string
	if v.ALPN != "" {
		alpn = strings.Split(v.ALPN, ",")
		for i := range alpn {
			alpn[i] = strings.TrimSpace(alpn[i])
		}
	}

	name := v.PS
	if name == "" && remark != "" {
		name = remark
	}

	now := time.Now()
	config := &model.ProxyConfig{
		Name:          name,
		Protocol:      model.ProtocolVMess,
		Address:       v.Add,
		Port:          port,
		UUID:          v.ID,
		AlterId:       alterId,
		Encryption:    encryption,
		Network:       network,
		TransportOpts: opts,
		Security:      security,
		SNI:           v.SNI,
		ALPN:          alpn,
		Fingerprint:   v.FP,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	return config, nil
}

// base64Decode tries standard, URL-safe, with and without padding.
func base64Decode(s string) ([]byte, error) {
	// Try standard with padding
	if decoded, err := base64.StdEncoding.DecodeString(s); err == nil {
		return decoded, nil
	}
	// Try standard without padding
	if decoded, err := base64.RawStdEncoding.DecodeString(s); err == nil {
		return decoded, nil
	}
	// Try URL-safe with padding
	if decoded, err := base64.URLEncoding.DecodeString(s); err == nil {
		return decoded, nil
	}
	// Try URL-safe without padding
	if decoded, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return decoded, nil
	}
	return nil, fmt.Errorf("failed all base64 decode attempts")
}

// toInt converts various types (string, float64, int) to int.
// JSON numbers unmarshal as float64 by default.
func toInt(v interface{}) (int, error) {
	switch val := v.(type) {
	case float64:
		return int(val), nil
	case int:
		return val, nil
	case string:
		return strconv.Atoi(val)
	case nil:
		return 0, nil
	default:
		return 0, fmt.Errorf("cannot convert %T to int", v)
	}
}

// mapNetwork normalizes transport network strings to model.Network.
func mapNetwork(s string) model.Network {
	switch strings.ToLower(s) {
	case "tcp":
		return model.NetworkTCP
	case "ws", "websocket":
		return model.NetworkWS
	case "grpc", "gun":
		return model.NetworkGRPC
	case "h2", "http":
		return model.NetworkH2
	case "httpupgrade":
		return model.NetworkHTTPUpgrade
	case "kcp", "mkcp":
		return model.NetworkKCP
	case "quic":
		return model.NetworkQUIC
	case "meek":
		return model.NetworkMeek
	case "splithttp":
		return model.NetworkSplitHTTP
	default:
		return model.NetworkTCP
	}
}
