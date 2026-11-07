package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

// Hub fans server events (optimizer status, live app logs) out to dashboard tabs over SSE.
type Hub struct {
	mu   sync.RWMutex
	subs map[chan []byte]struct{}
}

// NewHub creates an empty hub.
func NewHub() *Hub { return &Hub{subs: map[chan []byte]struct{}{}} }

// Broadcast sends event to every subscriber. A subscriber whose buffer is full
// misses the event rather than slowing everyone else down.
func (h *Hub) Broadcast(event any) {
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	msg := []byte(fmt.Sprintf("data: %s\n\n", data))
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subs {
		select {
		case ch <- msg:
		default:
		}
	}
}

// ServeHTTP streams events to one dashboard tab until it disconnects.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")

	ch := make(chan []byte, 100)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}()

	flusher.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-ch:
			w.Write(msg)
			flusher.Flush()
		}
	}
}
