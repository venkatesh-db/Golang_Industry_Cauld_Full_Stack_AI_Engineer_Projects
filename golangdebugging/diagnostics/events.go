package diagnostics

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Event is a structured log record retained for incident correlation.
// Fields should not contain credentials or tokens; the buffer intentionally
// retains records in process memory until they are evicted.
type Event struct {
	Time    time.Time      `json:"time"`
	Level   string         `json:"level"`
	Message string         `json:"message"`
	Fields  map[string]any `json:"fields,omitempty"`
}

// EventBuffer is a fixed-size, concurrent-safe ring buffer. Its memory use is
// bounded by the configured number of events (plus the size of their fields).
type EventBuffer struct {
	mu     sync.RWMutex
	events []Event
	next   int
	count  int
}

// NewEventBuffer creates a buffer that retains the newest capacity events.
func NewEventBuffer(capacity int) *EventBuffer {
	if capacity <= 0 {
		panic("diagnostics: event buffer capacity must be positive")
	}
	return &EventBuffer{events: make([]Event, capacity)}
}

// Add appends an event. A copy of Fields is stored so callers can reuse their map.
func (b *EventBuffer) Add(event Event) {
	if event.Time.IsZero() {
		event.Time = time.Now().UTC()
	}
	event.Fields = cloneFields(event.Fields)

	b.mu.Lock()
	b.events[b.next] = event
	b.next = (b.next + 1) % len(b.events)
	if b.count < len(b.events) {
		b.count++
	}
	b.mu.Unlock()
}

// Recent returns up to limit events in chronological order. The returned event
// values and their outer field maps are safe for the caller to modify.
func (b *EventBuffer) Recent(limit int) []Event {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if limit <= 0 || b.count == 0 {
		return nil
	}
	if limit > b.count {
		limit = b.count
	}
	start := (b.next - limit + len(b.events)) % len(b.events)
	result := make([]Event, 0, limit)
	for i := 0; i < limit; i++ {
		event := b.events[(start+i)%len(b.events)]
		event.Fields = cloneFields(event.Fields)
		result = append(result, event)
	}
	return result
}

func cloneFields(fields map[string]any) map[string]any {
	if len(fields) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(fields))
	for key, value := range fields {
		cloned[key] = value
	}
	return cloned
}

// CapturingHandler mirrors records to buffer before passing them to next. Only
// records at or above minimum are retained; the wrapped handler still receives
// every record it would normally receive.
func CapturingHandler(next slog.Handler, buffer *EventBuffer, minimum slog.Level) slog.Handler {
	if next == nil {
		panic("diagnostics: a wrapped slog handler is required")
	}
	if buffer == nil {
		panic("diagnostics: an event buffer is required")
	}
	return &capturingHandler{next: next, buffer: buffer, minimum: minimum}
}

type capturingHandler struct {
	next    slog.Handler
	buffer  *EventBuffer
	minimum slog.Level
	attrs   []slog.Attr
	groups  []string
}

func (h *capturingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *capturingHandler) Handle(ctx context.Context, record slog.Record) error {
	if record.Level >= h.minimum {
		fields := make(map[string]any)
		for _, attr := range h.attrs {
			addAttr(fields, h.groups, attr)
		}
		record.Attrs(func(attr slog.Attr) bool {
			addAttr(fields, h.groups, attr)
			return true
		})
		h.buffer.Add(Event{
			Time:    record.Time.UTC(),
			Level:   record.Level.String(),
			Message: record.Message,
			Fields:  fields,
		})
	}
	return h.next.Handle(ctx, record)
}

func (h *capturingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	copyOfAttrs := append(append([]slog.Attr(nil), h.attrs...), attrs...)
	return &capturingHandler{
		next:    h.next.WithAttrs(attrs),
		buffer:  h.buffer,
		minimum: h.minimum,
		attrs:   copyOfAttrs,
		groups:  append([]string(nil), h.groups...),
	}
}

func (h *capturingHandler) WithGroup(name string) slog.Handler {
	copyOfGroups := append(append([]string(nil), h.groups...), name)
	return &capturingHandler{
		next:    h.next.WithGroup(name),
		buffer:  h.buffer,
		minimum: h.minimum,
		attrs:   append([]slog.Attr(nil), h.attrs...),
		groups:  copyOfGroups,
	}
}

func addAttr(fields map[string]any, groups []string, attr slog.Attr) {
	attr.Value = attr.Value.Resolve()
	if attr.Equal(slog.Attr{}) {
		return
	}
	current := fields
	for _, group := range groups {
		if group == "" {
			continue
		}
		nested, ok := current[group].(map[string]any)
		if !ok {
			nested = make(map[string]any)
			current[group] = nested
		}
		current = nested
	}
	if attr.Value.Kind() == slog.KindGroup {
		for _, child := range attr.Value.Group() {
			addAttr(current, []string{attr.Key}, child)
		}
		return
	}
	if attr.Key != "" {
		current[attr.Key] = slogValue(attr.Value)
	}
}

func slogValue(value slog.Value) any {
	switch value.Kind() {
	case slog.KindBool:
		return value.Bool()
	case slog.KindDuration:
		return value.Duration().String()
	case slog.KindFloat64:
		return value.Float64()
	case slog.KindInt64:
		return value.Int64()
	case slog.KindString:
		return value.String()
	case slog.KindTime:
		return value.Time().UTC()
	case slog.KindUint64:
		return value.Uint64()
	case slog.KindAny:
		return fmt.Sprint(value.Any())
	default:
		return fmt.Sprint(value)
	}
}
