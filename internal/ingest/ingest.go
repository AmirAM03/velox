// Package ingest handles fetching and preprocessing of proxy configs from
// various sources (subscription URLs, file input, stdin paste).
package ingest

import (
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// DefaultHTTPTimeout is the timeout for subscription URL fetches.
const DefaultHTTPTimeout = 30 * time.Second

// Source represents a config source for tracing.
type Source struct {
	Type string // "subscription", "file", "paste"
	Name string // URL, filename, or "stdin"
}

func (s Source) String() string {
	return fmt.Sprintf("%s:%s", s.Type, s.Name)
}

// FetchSubscription downloads a subscription URL and returns the raw config
// lines. Handles base64-encoded responses (common for V2Ray subscriptions).
func FetchSubscription(url string, timeout time.Duration) (string, error) {
	if timeout == 0 {
		timeout = DefaultHTTPTimeout
	}

	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("fetch %q: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch %q: HTTP %d", url, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024)) // 10MB limit
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}

	raw := string(body)

	// Try to base64-decode the entire response (common for V2Ray subscriptions).
	// If decoding succeeds and the result looks like config lines, use it.
	if decoded, err := tryBase64Decode(raw); err == nil {
		if looksLikeConfigs(decoded) {
			slog.Debug("subscription response was base64-encoded", "url", url)
			return decoded, nil
		}
	}

	return raw, nil
}

// CleanRawContent preprocesses raw config text:
//   - Strips BOM
//   - Normalizes line endings (CRLF → LF)
//   - Removes comment lines (# and //)
//   - Removes empty lines
//   - Trims whitespace
func CleanRawContent(raw string) string {
	// Strip UTF-8 BOM
	raw = strings.TrimPrefix(raw, "\xef\xbb\xbf")

	// Normalize line endings
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")

	var cleaned []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)

		// Skip empty lines
		if line == "" {
			continue
		}

		// Skip comments
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}

		cleaned = append(cleaned, line)
	}

	return strings.Join(cleaned, "\n")
}

// tryBase64Decode attempts to base64-decode a string using multiple encodings.
func tryBase64Decode(s string) (string, error) {
	s = strings.TrimSpace(s)

	// Pad if necessary
	padded := s
	if m := len(s) % 4; m != 0 {
		padded += strings.Repeat("=", 4-m)
	}

	// Try standard encoding
	if decoded, err := base64.StdEncoding.DecodeString(padded); err == nil {
		return string(decoded), nil
	}

	// Try URL-safe encoding
	if decoded, err := base64.URLEncoding.DecodeString(padded); err == nil {
		return string(decoded), nil
	}

	// Try without padding
	if decoded, err := base64.RawStdEncoding.DecodeString(s); err == nil {
		return string(decoded), nil
	}

	return "", fmt.Errorf("not base64")
}

// looksLikeConfigs checks if decoded content looks like proxy config lines.
func looksLikeConfigs(s string) bool {
	schemes := []string{"vmess://", "vless://", "trojan://", "ss://", "hy2://", "hysteria2://", "tuic://", "wg://"}
	lines := strings.Split(s, "\n")
	matchCount := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		for _, scheme := range schemes {
			if strings.HasPrefix(strings.ToLower(line), scheme) {
				matchCount++
				break
			}
		}
	}
	// If at least 30% of non-empty lines look like configs, it's likely configs
	nonEmpty := 0
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			nonEmpty++
		}
	}
	if nonEmpty == 0 {
		return false
	}
	return float64(matchCount)/float64(nonEmpty) > 0.3
}
