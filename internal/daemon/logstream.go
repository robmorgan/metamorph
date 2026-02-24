package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
)

// LogBuffer is a thread-safe ring buffer for log lines.
type LogBuffer struct {
	mu    sync.Mutex
	lines []string
	size  int
	pos   int
	count int
}

// NewLogBuffer creates a ring buffer that holds up to size entries.
func NewLogBuffer(size int) *LogBuffer {
	return &LogBuffer{
		lines: make([]string, size),
		size:  size,
	}
}

// Write appends a line to the ring buffer.
func (b *LogBuffer) Write(line string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lines[b.pos] = line
	b.pos = (b.pos + 1) % b.size
	if b.count < b.size {
		b.count++
	}
}

// Lines returns all buffered lines in chronological order.
func (b *LogBuffer) Lines() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.count == 0 {
		return nil
	}
	result := make([]string, 0, b.count)
	start := 0
	if b.count == b.size {
		start = b.pos // oldest entry is at current write position
	}
	for i := 0; i < b.count; i++ {
		idx := (start + i) % b.size
		result = append(result, b.lines[idx])
	}
	return result
}

// LogStreamer manages a log buffer and broadcasts new lines to SSE clients.
type LogStreamer struct {
	buffer  *LogBuffer
	mu      sync.Mutex
	clients map[chan string]struct{}
}

// NewLogStreamer creates a LogStreamer with a ring buffer of the given size.
func NewLogStreamer(bufSize int) *LogStreamer {
	return &LogStreamer{
		buffer:  NewLogBuffer(bufSize),
		clients: make(map[chan string]struct{}),
	}
}

// Broadcast writes a line to the buffer and sends it to all connected clients.
func (s *LogStreamer) Broadcast(line string) {
	s.buffer.Write(line)

	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.clients {
		select {
		case ch <- line:
		default:
			// Drop if client can't keep up.
		}
	}
}

// Subscribe creates and registers a new client channel.
func (s *LogStreamer) Subscribe() chan string {
	ch := make(chan string, 256)
	s.mu.Lock()
	s.clients[ch] = struct{}{}
	s.mu.Unlock()
	return ch
}

// Unsubscribe removes and closes a client channel.
func (s *LogStreamer) Unsubscribe(ch chan string) {
	s.mu.Lock()
	delete(s.clients, ch)
	s.mu.Unlock()
}

// ServeHTTP implements the SSE endpoint for streaming daemon logs.
func (s *LogStreamer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Flush buffered history.
	for _, line := range s.buffer.Lines() {
		fmt.Fprintf(w, "data: %s\n\n", line)
	}
	flusher.Flush()

	// Subscribe for new lines.
	ch := s.Subscribe()
	defer s.Unsubscribe(ch)

	for {
		select {
		case line := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", line)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// BufferedHandler is an slog.Handler that broadcasts formatted log records
// to a LogStreamer while delegating to an inner handler.
type BufferedHandler struct {
	inner    slog.Handler
	streamer *LogStreamer
	attrs    []slog.Attr
	groups   []string
}

// NewBufferedHandler wraps an existing slog.Handler with log streaming.
func NewBufferedHandler(inner slog.Handler, streamer *LogStreamer) *BufferedHandler {
	return &BufferedHandler{
		inner:    inner,
		streamer: streamer,
	}
}

func (h *BufferedHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *BufferedHandler) Handle(ctx context.Context, r slog.Record) error {
	// Format: "15:04:05 LEVEL msg key=val ..."
	var b strings.Builder
	b.WriteString(r.Time.Format("15:04:05"))
	b.WriteByte(' ')
	b.WriteString(r.Level.String())
	b.WriteByte(' ')
	b.WriteString(r.Message)

	// Append pre-set attrs from WithAttrs.
	for _, a := range h.attrs {
		fmt.Fprintf(&b, " %s=%v", a.Key, a.Value)
	}

	// Append record attrs.
	r.Attrs(func(a slog.Attr) bool {
		fmt.Fprintf(&b, " %s=%v", a.Key, a.Value)
		return true
	})

	h.streamer.Broadcast(b.String())
	return h.inner.Handle(ctx, r)
}

func (h *BufferedHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &BufferedHandler{
		inner:    h.inner.WithAttrs(attrs),
		streamer: h.streamer,
		attrs:    append(h.attrs, attrs...),
		groups:   h.groups,
	}
}

func (h *BufferedHandler) WithGroup(name string) slog.Handler {
	return &BufferedHandler{
		inner:    h.inner.WithGroup(name),
		streamer: h.streamer,
		attrs:    h.attrs,
		groups:   append(h.groups, name),
	}
}

