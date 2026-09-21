package engine

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/AmirAM03/velox/internal/model"
)

// XrayConfig represents the top-level Xray configuration JSON structure.
type XrayConfig struct {
	Log       LogConfig        `json:"log"`
	Outbounds []OutboundConfig `json:"outbounds"`
}

type LogConfig struct {
	LogLevel string `json:"loglevel"`
}

type OutboundConfig struct {
	Tag            string          `json:"tag"`
	Protocol       string          `json:"protocol"`
	Settings       json.RawMessage `json:"settings"`
	StreamSettings *StreamSettings `json:"streamSettings,omitempty"`
}

type StreamSettings struct {
	Network         string                 `json:"network,omitempty"`
	Security        string                 `json:"security,omitempty"`
	TLSSettings     *TLSSettings           `json:"tlsSettings,omitempty"`
	RealitySettings *RealitySettings       `json:"realitySettings,omitempty"`
	WSSettings      *WSSettings            `json:"wsSettings,omitempty"`
	GRPCSettings    *GRPCSettings          `json:"grpcSettings,omitempty"`
	HTTPUpgrade     *HTTPUpgradeSettings   `json:"httpupgradeSettings,omitempty"`
	SplitHTTP       *SplitHTTPSettings     `json:"splithttpSettings,omitempty"`
	KCPSettings     *KCPSettings           `json:"kcpSettings,omitempty"`
	TCPSettings     map[string]interface{} `json:"tcpSettings,omitempty"`
}

type TLSSettings struct {
	ServerName    string   `json:"serverName,omitempty"`
	ALPN          []string `json:"alpn,omitempty"`
	Fingerprint   string   `json:"fingerprint,omitempty"`
	AllowInsecure bool     `json:"allowInsecure,omitempty"`
}

type RealitySettings struct {
	ServerName  string `json:"serverName,omitempty"`
	PublicKey   string `json:"publicKey,omitempty"`
	ShortId     string `json:"shortId,omitempty"`
	SpiderX     string `json:"spiderX,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

type WSSettings struct {
	Path    string            `json:"path,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

type GRPCSettings struct {
	ServiceName string `json:"serviceName,omitempty"`
	MultiMode   bool   `json:"multiMode,omitempty"`
}

type HTTPUpgradeSettings struct {
	Path string `json:"path,omitempty"`
	Host string `json:"host,omitempty"`
}

type SplitHTTPSettings struct {
	Path string `json:"path,omitempty"`
	Host string `json:"host,omitempty"`
}

type KCPSettings struct {
	Header map[string]string `json:"header,omitempty"`
	Seed   string            `json:"seed,omitempty"`
}

// BuildConfigJSON builds an Xray config JSON byte slice from a ProxyConfig.
func BuildConfigJSON(cfg *model.ProxyConfig) ([]byte, error) {
	outbound, err := buildOutbound(cfg)
	if err != nil {
		return nil, err
	}

	xrayCfg := XrayConfig{
		Log: LogConfig{
			LogLevel: "none",
		},
		Outbounds: []OutboundConfig{
			*outbound,
			{
				Tag:      "direct",
				Protocol: "freedom",
				Settings: json.RawMessage(`{}`),
			},
		},
	}

	return json.Marshal(xrayCfg)
}

func buildOutbound(cfg *model.ProxyConfig) (*OutboundConfig, error) {
	out := &OutboundConfig{
		Tag:      "proxy",
		Protocol: string(cfg.Protocol),
	}

	var settingsBytes []byte
	var err error

	switch cfg.Protocol {
	case model.ProtocolVLESS:
		settingsBytes, err = buildVLESSSettings(cfg)
	case model.ProtocolVMess:
		settingsBytes, err = buildVMessSettings(cfg)
	case model.ProtocolTrojan:
		settingsBytes, err = buildTrojanSettings(cfg)
	case model.ProtocolShadowsocks:
		settingsBytes, err = buildShadowsocksSettings(cfg)
	case model.ProtocolWireGuard:
		settingsBytes, err = buildWireGuardSettings(cfg)
	default:
		return nil, fmt.Errorf("unsupported protocol for xray outbound: %s", cfg.Protocol)
	}

	if err != nil {
		return nil, err
	}
	out.Settings = json.RawMessage(settingsBytes)

	// Stream settings (transport & TLS)
	stream := &StreamSettings{
		Network: string(cfg.Network),
	}
	if stream.Network == "" {
		stream.Network = "tcp"
	}

	// TLS / Security
	switch cfg.Security {
	case model.SecurityTLS:
		stream.Security = "tls"
		sni := cfg.SNI
		if sni == "" {
			sni = cfg.Address
		}
		stream.TLSSettings = &TLSSettings{
			ServerName:    sni,
			ALPN:          cfg.ALPN,
			Fingerprint:   cfg.Fingerprint,
			AllowInsecure: cfg.AllowInsecure,
		}
	case model.SecurityREALITY:
		stream.Security = "reality"
		sni := cfg.SNI
		if sni == "" {
			sni = cfg.Address
		}
		stream.RealitySettings = &RealitySettings{
			ServerName:  sni,
			PublicKey:   cfg.RealityPublicKey,
			ShortId:     cfg.RealityShortID,
			SpiderX:     cfg.RealitySpiderX,
			Fingerprint: cfg.Fingerprint,
		}
	case model.SecurityNone:
		stream.Security = "none"
	default:
		stream.Security = "none"
	}

	// Transport-specific options
	opts := cfg.TransportOpts
	if opts == nil {
		opts = make(map[string]string)
	}

	switch cfg.Network {
	case model.NetworkWS:
		ws := &WSSettings{
			Path: opts["path"],
		}
		if host, ok := opts["host"]; ok && host != "" {
			ws.Headers = map[string]string{"Host": host}
		}
		stream.WSSettings = ws

	case model.NetworkGRPC:
		grpc := &GRPCSettings{
			ServiceName: opts["serviceName"],
		}
		if opts["mode"] == "multi" {
			grpc.MultiMode = true
		}
		stream.GRPCSettings = grpc

	case model.NetworkHTTPUpgrade:
		stream.HTTPUpgrade = &HTTPUpgradeSettings{
			Path: opts["path"],
			Host: opts["host"],
		}

	case model.NetworkSplitHTTP:
		stream.SplitHTTP = &SplitHTTPSettings{
			Path: opts["path"],
			Host: opts["host"],
		}

	case model.NetworkKCP:
		kcp := &KCPSettings{
			Seed: opts["seed"],
		}
		if headerType, ok := opts["headerType"]; ok && headerType != "" {
			kcp.Header = map[string]string{"type": headerType}
		}
		stream.KCPSettings = kcp

	case model.NetworkTCP:
		if headerType, ok := opts["headerType"]; ok && strings.EqualFold(headerType, "http") {
			path := opts["path"]
			if path == "" {
				path = "/"
			}
			host := opts["host"]
			var hosts []string
			if host != "" {
				hosts = []string{host}
			}
			stream.TCPSettings = map[string]interface{}{
				"header": map[string]interface{}{
					"type": "http",
					"request": map[string]interface{}{
						"version": "1.1",
						"method":  "GET",
						"path":    []string{path},
						"headers": map[string]interface{}{
							"Host": hosts,
						},
					},
				},
			}
		}
	}

	out.StreamSettings = stream
	return out, nil
}

func buildVLESSSettings(cfg *model.ProxyConfig) ([]byte, error) {
	enc := cfg.Encryption
	if enc == "" {
		enc = "none"
	}
	user := map[string]interface{}{
		"id":         cfg.UUID,
		"encryption": enc,
		"level":      0,
	}
	if cfg.Flow != "" {
		user["flow"] = cfg.Flow
	}

	settings := map[string]interface{}{
		"vnext": []map[string]interface{}{
			{
				"address": cfg.Address,
				"port":    cfg.Port,
				"users":   []map[string]interface{}{user},
			},
		},
	}
	return json.Marshal(settings)
}

func buildVMessSettings(cfg *model.ProxyConfig) ([]byte, error) {
	sec := cfg.Encryption
	if sec == "" {
		sec = "auto"
	}
	user := map[string]interface{}{
		"id":       cfg.UUID,
		"alterId":  cfg.AlterId,
		"security": sec,
		"level":    0,
	}

	settings := map[string]interface{}{
		"vnext": []map[string]interface{}{
			{
				"address": cfg.Address,
				"port":    cfg.Port,
				"users":   []map[string]interface{}{user},
			},
		},
	}
	return json.Marshal(settings)
}

func buildTrojanSettings(cfg *model.ProxyConfig) ([]byte, error) {
	settings := map[string]interface{}{
		"servers": []map[string]interface{}{
			{
				"address":  cfg.Address,
				"port":     cfg.Port,
				"password": cfg.Password,
				"level":    0,
			},
		},
	}
	return json.Marshal(settings)
}

func buildShadowsocksSettings(cfg *model.ProxyConfig) ([]byte, error) {
	settings := map[string]interface{}{
		"servers": []map[string]interface{}{
			{
				"address":  cfg.Address,
				"port":     cfg.Port,
				"method":   cfg.Encryption,
				"password": cfg.Password,
				"level":    0,
			},
		},
	}
	return json.Marshal(settings)
}

func buildWireGuardSettings(cfg *model.ProxyConfig) ([]byte, error) {
	peer := map[string]interface{}{
		"publicKey": cfg.WGPublicKey,
		"endpoint":  fmt.Sprintf("%s:%d", cfg.Address, cfg.Port),
	}
	if len(cfg.WGReserved) > 0 {
		peer["reserved"] = cfg.WGReserved
	}

	settings := map[string]interface{}{
		"secretKey": cfg.WGPrivateKey,
		"address":   cfg.WGLocalAddr,
		"peers":     []map[string]interface{}{peer},
	}
	if cfg.WGMTu > 0 {
		settings["mtu"] = cfg.WGMTu
	}
	return json.Marshal(settings)
}
