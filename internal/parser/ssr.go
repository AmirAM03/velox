package parser

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/AmirAM03/velox/internal/model"
)

// ssrParser handles ssr:// (ShadowsocksR) URIs.
// Format: ssr://base64(host:port:protocol:method:obfs:base64pass/?obfsparam=...&protoparam=...&remarks=...)
type ssrParser struct{}

func init() {
	Register(&ssrParser{})
}

func (p *ssrParser) Scheme() string { return "ssr" }

func (p *ssrParser) Parse(uri string) (*model.ProxyConfig, error) {
	encoded := strings.TrimPrefix(uri, "ssr://")
	encoded = strings.TrimSpace(encoded)

	decodedBytes, err := base64Decode(encoded)
	if err != nil {
		return nil, fmt.Errorf("ssr base64 decode: %w", err)
	}
	decoded := string(decodedBytes)

	mainPart := decoded
	queryPart := ""
	if idx := strings.Index(decoded, "/?"); idx != -1 {
		mainPart = decoded[:idx]
		queryPart = decoded[idx+2:]
	} else if idx := strings.Index(decoded, "?"); idx != -1 {
		mainPart = decoded[:idx]
		queryPart = decoded[idx+1:]
	}

	parts := strings.Split(mainPart, ":")
	if len(parts) < 6 {
		return nil, fmt.Errorf("invalid ssr format: expected at least 6 fields")
	}

	host := parts[0]
	port, err := strconv.Atoi(parts[1])
	if err != nil || port <= 0 || port > 65535 {
		return nil, fmt.Errorf("invalid port in ssr: %s", parts[1])
	}

	// ssrProto := parts[2]
	method := parts[3]
	obfs := parts[4]
	passB64 := parts[5]

	password := passB64
	if decodedPass, err := base64Decode(passB64); err == nil {
		password = string(decodedPass)
	}

	// Parse query parameters
	name := ""
	opts := make(map[string]string)
	if obfs != "" && obfs != "plain" {
		opts["obfs"] = obfs
	}

	if queryPart != "" {
		vals, _ := url.ParseQuery(queryPart)
		if remarksB64 := vals.Get("remarks"); remarksB64 != "" {
			if decodedRemarks, err := base64Decode(remarksB64); err == nil {
				name = string(decodedRemarks)
			} else {
				name = remarksB64
			}
		}
		if obfsParamB64 := vals.Get("obfsparam"); obfsParamB64 != "" {
			if decoded, err := base64Decode(obfsParamB64); err == nil {
				opts["obfsparam"] = string(decoded)
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
		Network:       model.NetworkTCP,
		TransportOpts: opts,
		Security:      model.SecurityNone,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	return config, nil
}
