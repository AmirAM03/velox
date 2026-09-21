// Package model defines the canonical data structures used throughout Velox.
// All protocol-specific configs are normalized into these structs.
package model

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"time"
)

// Protocol represents a supported proxy protocol.
type Protocol string

const (
	ProtocolVMess       Protocol = "vmess"
	ProtocolVLESS       Protocol = "vless"
	ProtocolTrojan      Protocol = "trojan"
	ProtocolShadowsocks Protocol = "shadowsocks"
	ProtocolHysteria2   Protocol = "hysteria2"
	ProtocolTUIC        Protocol = "tuic"
	ProtocolWireGuard   Protocol = "wireguard"
)

// Network is the transport layer type.
type Network string

const (
	NetworkTCP        Network = "tcp"
	NetworkWS         Network = "ws"
	NetworkGRPC       Network = "grpc"
	NetworkH2         Network = "h2"
	NetworkHTTPUpgrade Network = "httpupgrade"
	NetworkKCP        Network = "kcp"
	NetworkQUIC       Network = "quic"
	NetworkMeek       Network = "meek"
	NetworkSplitHTTP  Network = "splithttp"
)

// Security is the TLS security mode.
type Security string

const (
	SecurityNone    Security = "none"
	SecurityTLS     Security = "tls"
	SecurityREALITY Security = "reality"
)

// ProxyConfig is the canonical, protocol-agnostic representation of a proxy
// configuration. All parsers normalize their output into this struct.
type ProxyConfig struct {
	// Identity
	ID       string   `json:"id"`       // Internal unique ID (SHA-256 hash of identity fields)
	Name     string   `json:"name"`     // Display name / remark
	RawURI   string   `json:"raw_uri"`  // Original unparsed URI
	Protocol Protocol `json:"protocol"` // Protocol type
	Source   string   `json:"source"`   // Where this config was ingested from

	// Server
	Address string `json:"address"` // Server hostname or IP
	Port    int    `json:"port"`    // Server port

	// Authentication (protocol-specific, at least one populated)
	UUID     string `json:"uuid,omitempty"`     // VMess/VLESS UUID
	Password string `json:"password,omitempty"` // Trojan password / SS password
	AlterId  int    `json:"alter_id,omitempty"` // VMess alterId (0 for AEAD)

	// Encryption
	Encryption string `json:"encryption,omitempty"` // VMess encryption / SS cipher
	Flow       string `json:"flow,omitempty"`       // VLESS flow (xtls-rprx-vision, etc.)

	// Transport
	Network       Network           `json:"network"`                  // Transport type
	TransportOpts map[string]string `json:"transport_opts,omitempty"` // Transport-specific options

	// TLS
	Security    Security `json:"security"`               // TLS mode
	SNI         string   `json:"sni,omitempty"`          // Server Name Indication
	ALPN        []string `json:"alpn,omitempty"`         // ALPN protocols
	Fingerprint string   `json:"fingerprint,omitempty"`  // uTLS fingerprint
	AllowInsecure bool   `json:"allow_insecure,omitempty"` // Skip cert verification

	// REALITY-specific
	RealityPublicKey string `json:"reality_public_key,omitempty"` // REALITY public key
	RealityShortID   string `json:"reality_short_id,omitempty"`   // REALITY short ID
	RealitySpiderX   string `json:"reality_spider_x,omitempty"`   // REALITY SpiderX

	// Hysteria2-specific
	Hysteria2Auth     string `json:"hy2_auth,omitempty"`     // Hysteria2 auth password
	Hysteria2Obfs     string `json:"hy2_obfs,omitempty"`     // Hysteria2 obfuscation type
	Hysteria2ObfsPass string `json:"hy2_obfs_pass,omitempty"` // Hysteria2 obfuscation password
	Hysteria2Up       string `json:"hy2_up,omitempty"`       // Hysteria2 upload bandwidth
	Hysteria2Down     string `json:"hy2_down,omitempty"`     // Hysteria2 download bandwidth

	// TUIC-specific
	TUICVersion       int    `json:"tuic_version,omitempty"`        // TUIC version (4 or 5)
	TUICCongestion    string `json:"tuic_congestion,omitempty"`     // Congestion control algorithm
	TUICUDPRelayMode  string `json:"tuic_udp_relay_mode,omitempty"` // UDP relay mode

	// WireGuard-specific
	WGPrivateKey string   `json:"wg_private_key,omitempty"` // WireGuard private key
	WGPublicKey  string   `json:"wg_public_key,omitempty"`  // WireGuard peer public key
	WGLocalAddr  []string `json:"wg_local_addr,omitempty"`  // WireGuard local addresses
	WGMTu        int      `json:"wg_mtu,omitempty"`         // WireGuard MTU
	WGReserved   []int    `json:"wg_reserved,omitempty"`    // WireGuard reserved bytes

	// Metadata
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Hash computes a SHA-256 identity hash from the fields that uniquely identify
// a proxy server. Two configs with different names but same server/auth/transport
// are considered duplicates.
func (c *ProxyConfig) Hash() string {
	identity := fmt.Sprintf("%s|%s|%d|%s|%s|%s|%s|%s",
		c.Protocol,
		strings.ToLower(c.Address),
		c.Port,
		c.UUID,
		c.Password,
		c.Encryption,
		c.Network,
		c.Security,
	)
	sum := sha256.Sum256([]byte(identity))
	return fmt.Sprintf("%x", sum[:16]) // 32-char hex
}

// ComputeID sets the ID field based on the identity hash.
// Call this after all identity fields are populated.
func (c *ProxyConfig) ComputeID() {
	c.ID = c.Hash()
}

// DisplayName returns a human-readable name for the config.
// Falls back to protocol://address:port if Name is empty.
func (c *ProxyConfig) DisplayName() string {
	if c.Name != "" {
		return c.Name
	}
	return fmt.Sprintf("%s://%s:%d", c.Protocol, c.Address, c.Port)
}
