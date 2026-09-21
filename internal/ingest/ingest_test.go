package ingest

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCleanRawContent(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{
			name:   "strips comments and empty lines",
			input:  "# comment\n\nvmess://abc\n// another comment\nvless://def\n\n",
			expect: "vmess://abc\nvless://def",
		},
		{
			name:   "normalizes CRLF",
			input:  "vmess://abc\r\nvless://def\r\n",
			expect: "vmess://abc\nvless://def",
		},
		{
			name:   "strips BOM",
			input:  "\xef\xbb\xbfvmess://abc",
			expect: "vmess://abc",
		},
		{
			name:   "trims whitespace",
			input:  "  vmess://abc  \n  vless://def  ",
			expect: "vmess://abc\nvless://def",
		},
		{
			name:   "empty input",
			input:  "",
			expect: "",
		},
		{
			name:   "only comments",
			input:  "# comment\n// another",
			expect: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CleanRawContent(tt.input)
			if got != tt.expect {
				t.Errorf("CleanRawContent() = %q, want %q", got, tt.expect)
			}
		})
	}
}

func TestFetchSubscription(t *testing.T) {
	// Mock server that returns plain text configs
	plainServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("vmess://abc\nvless://def\n"))
	}))
	defer plainServer.Close()

	raw, err := FetchSubscription(plainServer.URL, 0)
	if err != nil {
		t.Fatalf("FetchSubscription() error: %v", err)
	}

	if raw != "vmess://abc\nvless://def\n" {
		t.Errorf("FetchSubscription() = %q, unexpected", raw)
	}
}

func TestFetchSubscriptionBase64(t *testing.T) {
	// base64("vmess://abc\nvless://def\n") = "dm1lc3M6Ly9hYmMKdmxlc3M6Ly9kZWYK"
	b64Server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("dm1lc3M6Ly9hYmMKdmxlc3M6Ly9kZWYK"))
	}))
	defer b64Server.Close()

	raw, err := FetchSubscription(b64Server.URL, 0)
	if err != nil {
		t.Fatalf("FetchSubscription() error: %v", err)
	}

	// Should auto-detect and decode base64
	if raw != "vmess://abc\nvless://def\n" {
		t.Errorf("FetchSubscription() = %q, expected decoded base64", raw)
	}
}

func TestFetchSubscription404(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	_, err := FetchSubscription(server.URL, 0)
	if err == nil {
		t.Fatal("expected error for 404 response")
	}
}

func TestLooksLikeConfigs(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect bool
	}{
		{"valid configs", "vmess://abc\nvless://def\nss://ghi", true},
		{"minority configs", "vmess://abc\nrandom text\nmore random\nyet more random", false}, // 1/4 = 25% < 30%
		{"borderline true", "vmess://abc\nrandom text\nmore random", true}, // 1/3 = 33% > 30%
		{"empty", "", false},
		{"all comments", "# comment\n// comment", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := looksLikeConfigs(tt.input)
			if got != tt.expect {
				t.Errorf("looksLikeConfigs() = %v, want %v", got, tt.expect)
			}
		})
	}
}
