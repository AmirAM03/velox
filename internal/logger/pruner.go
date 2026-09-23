package logger

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/AmirAM03/velox/internal/storage"
)

// ParseRetention converts retention strings (e.g., "24h", "3d", "7d", "30d", "90d") into a time.Duration.
func ParseRetention(s string) (time.Duration, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" || s == "default" {
		return 7 * 24 * time.Hour, nil
	}

	// Days shorthand: e.g. "7d", "30d", "1d"
	if strings.HasSuffix(s, "d") {
		numStr := strings.TrimSuffix(s, "d")
		days, err := strconv.Atoi(numStr)
		if err != nil || days <= 0 {
			return 0, fmt.Errorf("invalid retention days format: %q", s)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}

	// Weeks shorthand: e.g. "1w", "2w"
	if strings.HasSuffix(s, "w") {
		numStr := strings.TrimSuffix(s, "w")
		weeks, err := strconv.Atoi(numStr)
		if err != nil || weeks <= 0 {
			return 0, fmt.Errorf("invalid retention weeks format: %q", s)
		}
		return time.Duration(weeks) * 7 * 24 * time.Hour, nil
	}

	// Months shorthand: e.g. "1m" (approximated as 30 days)
	if strings.HasSuffix(s, "m") && !strings.HasSuffix(s, "min") && !strings.HasSuffix(s, "ms") {
		numStr := strings.TrimSuffix(s, "m")
		months, err := strconv.Atoi(numStr)
		if err == nil && months > 0 {
			return time.Duration(months) * 30 * 24 * time.Hour, nil
		}
	}

	// Fallback to standard Go duration parsing (e.g. "48h")
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid retention duration: %w", err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("retention duration must be positive")
	}
	return d, nil
}

// StartPruner launches an automated background worker that cleans up expired logs.
func StartPruner(ctx context.Context, store *storage.Store, log *slog.Logger, interval time.Duration) {
	if store == nil {
		return
	}
	if interval <= 0 {
		interval = 1 * time.Hour
	}
	if log == nil {
		log = slog.Default()
	}

	go func() {
		pruneOnce := func() {
			retentionStr, err := store.GetSetting("log_retention", "7d")
			if err != nil || retentionStr == "" {
				retentionStr = "7d"
			}

			dur, err := ParseRetention(retentionStr)
			if err != nil {
				log.Warn("invalid log retention setting, defaulting to 7d",
					slog.String("source", "system"),
					slog.String("raw_value", retentionStr),
					slog.String("error", err.Error()),
				)
				dur = 7 * 24 * time.Hour
			}

			deleted, err := store.PruneLogs(dur)
			if err != nil {
				log.Error("failed to prune expired logs",
					slog.String("source", "system"),
					slog.String("error", err.Error()),
				)
				return
			}

			if deleted > 0 {
				log.Info("pruned expired application logs",
					slog.String("source", "system"),
					slog.Int64("deleted_records", deleted),
					slog.String("retention", retentionStr),
				)
			}
		}

		// Initial run on startup
		pruneOnce()

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pruneOnce()
			}
		}
	}()
}
