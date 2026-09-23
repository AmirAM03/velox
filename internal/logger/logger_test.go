package logger

import (
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/AmirAM03/velox/internal/storage"
)

func TestParseRetention(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
		hasErr   bool
	}{
		{"24h", 24 * time.Hour, false},
		{"3d", 72 * time.Hour, false},
		{"7d", 7 * 24 * time.Hour, false},
		{"1w", 7 * 24 * time.Hour, false},
		{"30d", 30 * 24 * time.Hour, false},
		{"1m", 30 * 24 * time.Hour, false},
		{"invalid", 0, true},
		{"-5d", 0, true},
	}

	for _, tt := range tests {
		got, err := ParseRetention(tt.input)
		if (err != nil) != tt.hasErr {
			t.Errorf("ParseRetention(%q) error = %v, expectedErr = %v", tt.input, err, tt.hasErr)
			continue
		}
		if !tt.hasErr && got != tt.expected {
			t.Errorf("ParseRetention(%q) = %v, expected %v", tt.input, got, tt.expected)
		}
	}
}

func TestDBHandler_LoggingAndBatching(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "handler_test.db")
	store, err := storage.Open(dbPath, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	handler := NewDBHandler(Config{
		Store:       store,
		Level:       slog.LevelDebug,
		BufferSize:  100,
		BatchSize:   10,
		FlushPeriod: 50 * time.Millisecond,
	})

	var liveReceived []*storage.LogRecord
	handler.AddBroadcast(func(rec *storage.LogRecord) {
		liveReceived = append(liveReceived, rec)
	})

	l := slog.New(handler).With("source", "engine")

	l.Info("Engine initialized", "port", 1080)
	l.Error("Engine failed to connect", "target", "1.1.1.1", "code", 500)

	// Close handler to flush pending records
	if err := handler.Close(); err != nil {
		t.Fatalf("Close handler: %v", err)
	}

	if len(liveReceived) != 2 {
		t.Errorf("expected 2 live received records, got %d", len(liveReceived))
	}

	// Verify records in DB
	records, total, err := store.QueryLogs(storage.LogQueryFilter{})
	if err != nil {
		t.Fatalf("QueryLogs: %v", err)
	}
	if total != 2 || len(records) != 2 {
		t.Fatalf("expected 2 stored logs, got %d (total=%d)", len(records), total)
	}

	if records[0].Source != "engine" {
		t.Errorf("expected source 'engine', got %q", records[0].Source)
	}
}
