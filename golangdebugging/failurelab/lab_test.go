package failurelab

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLabIsHiddenWhenDisabled(t *testing.T) {
	lab := New(Config{})
	request := httptest.NewRequest(http.MethodPost, "/internal/failure-lab/memory-pressure", nil)
	request.SetPathValue("experiment", "memory-pressure")
	response := httptest.NewRecorder()
	lab.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.Code)
	}
}

func TestLabRequiresTokenAndBoundsResources(t *testing.T) {
	lab := New(Config{Enabled: true, Token: "secret", MaxMemoryMiB: 2, MaxLeakedGoroutines: 3})

	unauthorized := httptest.NewRequest(http.MethodPost, "/internal/failure-lab/memory-pressure?mib=20", nil)
	unauthorized.SetPathValue("experiment", "memory-pressure")
	response := httptest.NewRecorder()
	lab.Handler().ServeHTTP(response, unauthorized)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want 401", response.Code)
	}

	invoke(t, lab, "memory-pressure", "?mib=20")
	invoke(t, lab, "goroutine-leak", "?count=20")
	lab.mu.Lock()
	if len(lab.allocations) != 1 || len(lab.allocations[0]) != 2<<20 || lab.leaked != 3 {
		t.Fatalf("limits not enforced: allocations=%d bytes=%d leaked=%d", len(lab.allocations), len(lab.allocations[0]), lab.leaked)
	}
	lab.mu.Unlock()

	invoke(t, lab, "reset", "")
	time.Sleep(10 * time.Millisecond)
	lab.mu.Lock()
	defer lab.mu.Unlock()
	if len(lab.allocations) != 0 || lab.leaked != 0 {
		t.Fatalf("reset failed: allocations=%d leaked=%d", len(lab.allocations), lab.leaked)
	}
}

func invoke(t *testing.T, lab *Lab, experiment, query string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/internal/failure-lab/"+experiment+query, nil)
	request.Header.Set("X-Failure-Lab-Token", "secret")
	request.SetPathValue("experiment", experiment)
	response := httptest.NewRecorder()
	lab.Handler().ServeHTTP(response, request)
	if response.Code < 200 || response.Code >= 300 {
		t.Fatalf("%s status = %d body=%s", experiment, response.Code, response.Body.String())
	}
}
