// Package parser provides config URI parsers that normalize protocol-specific
// URIs (vmess://, vless://, etc.) into the canonical model.ProxyConfig struct.
package parser

import (
	"fmt"
	"strings"

	"github.com/AmirAM03/velox/internal/model"
)

// Parser converts a raw URI string into a ProxyConfig.
// Each protocol has its own Parser implementation.
type Parser interface {
	// Scheme returns the URI scheme this parser handles (e.g., "vmess", "vless").
	Scheme() string

	// Parse converts a raw URI into a ProxyConfig.
	// Returns an error if the URI is malformed or missing required fields.
	Parse(uri string) (*model.ProxyConfig, error)
}

// registry holds all registered parsers, keyed by scheme.
var registry = map[string]Parser{}

// Register adds a parser to the global registry. Called by init() in each parser file.
func Register(p Parser) {
	registry[p.Scheme()] = p
}

// ParseURI detects the scheme of a URI and dispatches to the appropriate parser.
// Returns ErrUnknownScheme if no parser is registered for the scheme.
func ParseURI(uri string) (*model.ProxyConfig, error) {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return nil, fmt.Errorf("empty URI")
	}

	scheme, _, found := strings.Cut(uri, "://")
	if !found {
		return nil, fmt.Errorf("invalid URI format (no scheme): %q", truncate(uri, 80))
	}

	scheme = strings.ToLower(scheme)
	p, ok := registry[scheme]
	if !ok {
		return nil, &ErrUnknownScheme{Scheme: scheme}
	}

	config, err := p.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", scheme, err)
	}

	config.RawURI = uri
	config.ComputeID()
	return config, nil
}

// ParseMany parses multiple URIs, one per line. It skips empty lines, comments
// (lines starting with # or //), and logs parse errors without failing.
// Returns all successfully parsed configs and the count of failures.
func ParseMany(raw string) (configs []*model.ProxyConfig, failures int) {
	configs, failures, _ = ParseManyDetailed(raw)
	return configs, failures
}

// ParseManyDetailed parses multiple URIs, one per line.
// It returns:
// - configs: all unique, successfully parsed ProxyConfigs
// - failures: count of unparseable lines
// - duplicates: count of redundant configs skipped in this batch
func ParseManyDetailed(raw string) (configs []*model.ProxyConfig, failures int, duplicates int) {
	lines := strings.Split(raw, "\n")
	seen := make(map[string]struct{})

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}

		config, err := ParseURI(line)
		if err != nil {
			failures++
			continue
		}

		// Dedup by identity hash
		if _, dup := seen[config.ID]; dup {
			duplicates++
			continue
		}
		seen[config.ID] = struct{}{}
		configs = append(configs, config)
	}
	return configs, failures, duplicates
}

// Schemes returns all registered scheme names.
func Schemes() []string {
	schemes := make([]string, 0, len(registry))
	for s := range registry {
		schemes = append(schemes, s)
	}
	return schemes
}

// ErrUnknownScheme is returned when no parser is registered for a URI scheme.
type ErrUnknownScheme struct {
	Scheme string
}

func (e *ErrUnknownScheme) Error() string {
	return fmt.Sprintf("unknown scheme: %q (supported: %s)", e.Scheme, strings.Join(Schemes(), ", "))
}

// truncate shortens a string for error messages.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
