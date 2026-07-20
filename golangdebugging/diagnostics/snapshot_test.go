package diagnostics

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestEventBufferRetainsNewestAndCopiesFields(t *testing.T) {
	buffer := NewEventBuffer(3)
	fields := map[string]any{"request_id": "first"}
	buffer.Add(Event{Message: "one", Fields: fields})
	fields["request_id"] = "mutated"
	buffer.Add(Event{Message: "two"})
	buffer.Add(Event{Message: "three"})

	events := buffer.Recent(10)
	if len(events) != 3 || events[0].Fields["request_id"] != "first" {
		t.Fatalf("stored event should retain the original fields: %#v", events)
	}
	events[0].Fields["request_id"] = "caller change"
	if got := buffer.Recent(1)[0].Message; got != "three" {
		t.Fatalf("Recent() returned events out of order: %q", got)
	}
	if got := buffer.Recent(3)[0].Fields["request_id"]; got != "first" {
		t.Fatalf("Recent() leaked its field map: %q", got)
	}
}

func TestEventBufferSupportsConcurrentLoggingAndSnapshots(t *testing.T) {
	buffer := NewEventBuffer(32)
	var writers sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		writers.Add(1)
		go func(worker int) {
			defer writers.Done()
			for event := 0; event < 200; event++ {
				buffer.Add(Event{Message: "error", Fields: map[string]any{"worker": worker, "event": event}})
				_ = buffer.Recent(10)
			}
		}(worker)
	}
	writers.Wait()
	if got := len(buffer.Recent(100)); got != 32 {
		t.Fatalf("retained %d events, want 32", got)
	}
}

func TestCapturingHandlerRetainsStructuredError(t *testing.T) {
	buffer := NewEventBuffer(4)
	logger := slog.New(CapturingHandler(slog.NewTextHandler(io.Discard, nil), buffer, slog.LevelError))
	logger.WithGroup("request").Error("database timed out", "trace_id", "abc-123", "attempt", 2)
	logger.Info("not retained")

	events := buffer.Recent(4)
	if len(events) != 1 {
		t.Fatalf("captured %d events, want 1", len(events))
	}
	request, ok := events[0].Fields["request"].(map[string]any)
	if !ok || request["trace_id"] != "abc-123" || request["attempt"] != int64(2) {
		t.Fatalf("unexpected fields: %#v", events[0].Fields)
	}
}

func TestGroupGoroutinesAggregatesIdenticalStacks(t *testing.T) {
	dump := "goroutine 17 [chan receive]:\nworker()\n\t/app/worker.go:42 +0x1\n\ngoroutine 18 [chan receive]:\nworker()\n\t/app/worker.go:42 +0x1\n\ngoroutine 1 [running]:\nmain()\n\t/app/main.go:9 +0x1\n"
	groups := groupGoroutines(dump)
	if len(groups) != 2 || groups[0].Count != 2 || groups[0].State != "chan receive" {
		t.Fatalf("groupGoroutines() = %#v", groups)
	}
	if strings.Contains(groups[0].Stack, "17") || strings.Contains(groups[0].Stack, "18") {
		t.Fatalf("goroutine IDs should be normalized: %q", groups[0].Stack)
	}
}

func TestServiceCapturesEventsAndRuntime(t *testing.T) {
	buffer := NewEventBuffer(2)
	buffer.Add(Event{Message: "dependency failed", Level: "ERROR"})
	service := NewService(Options{Events: buffer, Now: func() time.Time { return time.Unix(1700000000, 0) }})

	snapshot, err := service.Capture(context.Background(), false)
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	if !snapshot.CapturedAt.Equal(time.Unix(1700000000, 0).UTC()) || snapshot.Runtime.Goroutines < 1 || len(snapshot.Events) != 1 {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
	if snapshot.Goroutines != nil {
		t.Fatal("goroutines should not be captured unless requested")
	}
}

func TestHandlerRequiresAuthorizationAndRateLimits(t *testing.T) {
	now := time.Unix(1700000000, 0)
	service := NewService(Options{Now: func() time.Time { return now }})
	handler := NewHandler(HTTPOptions{
		Service:           service,
		Authorize:         func(request *http.Request) bool { return request.Header.Get("X-Internal-Token") == "allowed" },
		RequestsPerMinute: 1,
		Now:               func() time.Time { return now },
	})

	unauthorized := httptest.NewRequest(http.MethodGet, "/internal/diagnostics/snapshot", nil)
	unauthorized.RemoteAddr = "192.0.2.10:1234"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, unauthorized)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want %d", response.Code, http.StatusUnauthorized)
	}

	authorized := httptest.NewRequest(http.MethodGet, "/internal/diagnostics/snapshot", nil)
	authorized.Header.Set("X-Internal-Token", "allowed")
	authorized.RemoteAddr = "192.0.2.10:1234"
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, authorized)
	if response.Code != http.StatusOK {
		t.Fatalf("authorized status = %d body=%s", response.Code, response.Body.String())
	}
	var snapshot Snapshot
	if err := json.NewDecoder(bytes.NewReader(response.Body.Bytes())).Decode(&snapshot); err != nil {
		t.Fatalf("invalid response JSON: %v", err)
	}

	again := httptest.NewRequest(http.MethodGet, "/internal/diagnostics/snapshot", nil)
	again.Header.Set("X-Internal-Token", "allowed")
	again.RemoteAddr = "192.0.2.10:1234"
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, again)
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("rate limited status = %d, want %d", response.Code, http.StatusTooManyRequests)
	}
}
