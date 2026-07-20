package social

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHomeRendersArchitectureUI(t *testing.T) {
	server, err := NewHTTPServer(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewHTTPServer: %v", err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	server.Router().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	for _, needle := range []string{"Every heart becomes a", "Transactional outbox", "Live pipeline"} {
		if !strings.Contains(recorder.Body.String(), needle) {
			t.Fatalf("rendered UI is missing %q", needle)
		}
	}
}
