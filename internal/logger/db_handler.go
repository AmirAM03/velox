// Package logger provides a high-performance SQLite-backed structured logger
// implementing slog.Handler with asynchronous batching, console forwarding,
// real-time SSE broadcasting, and automated background retention pruning.
package logger

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/AmirAM03/velox/internal/storage"
)

// BroadcastFunc is called whenever a new log entry is handled.
type BroadcastFunc func(record *storage.LogRecord)

// DBHandler is an slog.Handler that persists logs to SQLite in background batches,
// mirrors output to a console handler, and broadcasts to live web consumers.
type DBHandler struct {
	store      *storage.Store
	console    slog.Handler
	minLevel   slog.Level
	attrs      []slog.Attr
	group      string
	source     string
	ch         chan *storage.LogRecord
	stopCh     chan struct{}
	doneCh     chan struct{}
	broadcasts []BroadcastFunc
	mu         sync.RWMutex
}

// Config configures the DBHandler.
type Config struct {
	Store       *storage.Store
	Level       slog.Level
	JSONFormat  bool
	ConsoleOut  io.Writer
	BufferSize  int
	BatchSize   int
	FlushPeriod time.Duration
}

// NewDBHandler creates an slog.Handler that persists logs into the given storage.Store.
func NewDBHandler(cfg Config) *DBHandler {
	if cfg.ConsoleOut == nil {
		cfg.ConsoleOut = os.Stderr
	}
	if cfg.BufferSize <= 0 {
		cfg.BufferSize = 2048
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 50
	}
	if cfg.FlushPeriod <= 0 {
		cfg.FlushPeriod = 200 * time.Millisecond
	}

	opts := &slog.HandlerOptions{Level: cfg.Level}
	var console slog.Handler
	if cfg.JSONFormat {
		console = slog.NewJSONHandler(cfg.ConsoleOut, opts)
	} else {
		console = slog.NewTextHandler(cfg.ConsoleOut, opts)
	}

	h := &DBHandler{
		store:    cfg.Store,
		console:  console,
		minLevel: cfg.Level,
		source:   "system",
		ch:       make(chan *storage.LogRecord, cfg.BufferSize),
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}

	if cfg.Store != nil {
		go h.worker(cfg.BatchSize, cfg.FlushPeriod)
	} else {
		close(h.doneCh)
	}

	return h
}

// AddBroadcast registers a listener for real-time log messages.
func (h *DBHandler) AddBroadcast(fn BroadcastFunc) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.broadcasts = append(h.broadcasts, fn)
}

// Enabled reports whether the handler emits log records at the given level.
func (h *DBHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.minLevel
}

// Handle processes an slog.Record.
func (h *DBHandler) Handle(ctx context.Context, r slog.Record) error {
	// 1. Forward to console handler
	if h.console != nil && h.console.Enabled(ctx, r.Level) {
		_ = h.console.Handle(ctx, r)
	}

	// 2. Extract source and attributes
	source := h.source
	attrsMap := make(map[string]any)

	// Add handler pre-formatted attrs
	for _, a := range h.attrs {
		if a.Key == "source" {
			source = fmt.Sprint(a.Value.Any())
		} else {
			attrsMap[a.Key] = a.Value.Any()
		}
	}

	// Add record attrs
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "source" {
			source = fmt.Sprint(a.Value.Any())
		} else {
			attrsMap[a.Key] = a.Value.Any()
		}
		return true
	})

	var attrsJSON string
	if len(attrsMap) > 0 {
		if b, err := json.Marshal(attrsMap); err == nil {
			attrsJSON = string(b)
		}
	}
	if attrsJSON == "" {
		attrsJSON = "{}"
	}

	rec := &storage.LogRecord{
		Timestamp: r.Time,
		Level:     r.Level.String(),
		Source:    source,
		Message:   r.Message,
		Attrs:     attrsMap,
		AttrsJSON: attrsJSON,
	}

	// 3. Notify real-time listeners
	h.mu.RLock()
	listeners := h.broadcasts
	h.mu.RUnlock()
	for _, fn := range listeners {
		fn(rec)
	}

	// 4. Enqueue for persistent SQLite batching
	if h.store != nil {
		select {
		case h.ch <- rec:
		default:
			// Non-blocking drop if buffer full to never block network pipelines
		}
	}

	return nil
}

// WithAttrs returns a new handler whose attributes include the given attributes.
func (h *DBHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	var newAttrs []slog.Attr
	newAttrs = append(newAttrs, h.attrs...)
	newAttrs = append(newAttrs, attrs...)

	source := h.source
	for _, a := range attrs {
		if a.Key == "source" {
			source = fmt.Sprint(a.Value.Any())
		}
	}

	var console slog.Handler
	if h.console != nil {
		console = h.console.WithAttrs(attrs)
	}

	return &DBHandler{
		store:      h.store,
		console:    console,
		minLevel:   h.minLevel,
		attrs:      newAttrs,
		group:      h.group,
		source:     source,
		ch:         h.ch,
		stopCh:     h.stopCh,
		doneCh:     h.doneCh,
		broadcasts: h.broadcasts,
	}
}

// WithGroup returns a new handler with the given group name.
func (h *DBHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	var console slog.Handler
	if h.console != nil {
		console = h.console.WithGroup(name)
	}

	return &DBHandler{
		store:      h.store,
		console:    console,
		minLevel:   h.minLevel,
		attrs:      h.attrs,
		group:      h.group + "." + name,
		source:     h.source,
		ch:         h.ch,
		stopCh:     h.stopCh,
		doneCh:     h.doneCh,
		broadcasts: h.broadcasts,
	}
}

// worker batches log entries and inserts them to SQLite.
func (h *DBHandler) worker(batchSize int, flushPeriod time.Duration) {
	defer close(h.doneCh)

	ticker := time.NewTicker(flushPeriod)
	defer ticker.Stop()

	batch := make([]*storage.LogRecord, 0, batchSize)

	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := h.store.InsertLogsBatch(batch); err != nil {
			// Write error to stderr directly to avoid recursive loop
			fmt.Fprintf(os.Stderr, "velox: failed to batch write logs to SQLite: %v\n", err)
		}
		batch = make([]*storage.LogRecord, 0, batchSize)
	}

	for {
		select {
		case <-h.stopCh:
			// Drain remaining logs
			for {
				select {
				case rec := <-h.ch:
					batch = append(batch, rec)
					if len(batch) >= batchSize {
						flush()
					}
				default:
					flush()
					return
				}
			}
		case <-ticker.C:
			flush()
		case rec := <-h.ch:
			batch = append(batch, rec)
			if len(batch) >= batchSize {
				flush()
			}
		}
	}
}

// Close gracefully flushes buffered logs and stops the worker.
func (h *DBHandler) Close() error {
	select {
	case <-h.stopCh:
		return nil
	default:
		close(h.stopCh)
		<-h.doneCh
		return nil
	}
}
