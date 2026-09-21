package parser

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// ParsedProxyURI holds the standard components of a proxy URI:
// scheme://[userinfo@]host[:port][/?query][#fragment]
type ParsedProxyURI struct {
	Scheme   string
	UserInfo string // UUID, password, auth token, or base64 userinfo
	Host     string
	Port     int
	Query    url.Values
	Fragment string // Remark / Name
}

// SplitProxyURI cleanly decomposes a proxy URI into its components.
// Unlike standard net/url.Parse, it does not fail on unescaped characters
// (spaces, <, [, ], etc.) in userinfo or fragment, and handles multiple '#' or '@'
// characters predictably.
func SplitProxyURI(raw string) (*ParsedProxyURI, error) {
	raw = strings.TrimSpace(raw)
	scheme, rest, found := strings.Cut(raw, "://")
	if !found {
		return nil, fmt.Errorf("invalid URI format: missing scheme")
	}
	scheme = strings.ToLower(scheme)

	// 1. Fragment: extract from the LAST '#' (the proxy remark/name)
	fragment := ""
	if idx := strings.LastIndex(rest, "#"); idx != -1 {
		fragment = rest[idx+1:]
		rest = rest[:idx]
		if unescaped, err := url.QueryUnescape(fragment); err == nil {
			fragment = unescaped
		} else if unescaped, err := url.PathUnescape(fragment); err == nil {
			fragment = unescaped
		}
	}

	// 2. Query: extract from the FIRST '?'
	query := make(url.Values)
	if idx := strings.Index(rest, "?"); idx != -1 {
		qStr := rest[idx+1:]
		rest = rest[:idx]
		var err error
		query, err = url.ParseQuery(qStr)
		if err != nil {
			// Fallback: parse manually if ParseQuery encounters odd formatting
			for _, part := range strings.Split(qStr, "&") {
				k, v, _ := strings.Cut(part, "=")
				if unescapedK, err := url.QueryUnescape(k); err == nil {
					k = unescapedK
				}
				if unescapedV, err := url.QueryUnescape(v); err == nil {
					v = unescapedV
				}
				query.Add(k, v)
			}
		}
	}

	// 3. Clean trailing slashes
	rest = strings.TrimSuffix(rest, "/")

	// 4. UserInfo vs HostPort
	// In proxy URIs, hostPort cannot contain '@', while userinfo might contain '@'
	// (e.g. "@channel" or "ID: @user@host:port"). Therefore the LAST '@' separates userinfo from hostPort.
	userInfo := ""
	hostPort := rest
	if atIdx := strings.LastIndex(rest, "@"); atIdx != -1 {
		userInfo = rest[:atIdx]
		hostPort = rest[atIdx+1:]
	}

	// Unescape userinfo if URL-encoded
	if unescaped, err := url.QueryUnescape(userInfo); err == nil {
		userInfo = unescaped
	}

	hostPort = strings.TrimSpace(hostPort)
	if hostPort == "" {
		return nil, fmt.Errorf("missing server address")
	}

	// 5. Host and Port
	var host string
	var port int

	if h, p, err := net.SplitHostPort(hostPort); err == nil {
		host = h
		port, _ = strconv.Atoi(p)
	} else {
		// Could be missing port, or IPv6 literal without brackets, or IPv4:port
		colonIdx := strings.LastIndex(hostPort, ":")
		if colonIdx != -1 && !strings.Contains(hostPort, "]") {
			host = hostPort[:colonIdx]
			port, _ = strconv.Atoi(hostPort[colonIdx+1:])
		} else {
			host = strings.Trim(hostPort, "[]")
		}
	}

	return &ParsedProxyURI{
		Scheme:   scheme,
		UserInfo: userInfo,
		Host:     host,
		Port:     port,
		Query:    query,
		Fragment: fragment,
	}, nil
}
