package events

import (
	"encoding/json"
	"regexp"
	"testing"
)

func TestNewBuildsVersionedEnvelope(t *testing.T) {
	envelope, err := New("ride.requested", "ride-api", "correlation-1", "cause-1", RideRequested{RideID: "ride-1"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if envelope.EventType != "ride.requested" || envelope.Producer != "ride-api" || envelope.SchemaVersion != 1 {
		t.Fatalf("unexpected envelope metadata: %#v", envelope)
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(envelope.EventID) {
		t.Fatalf("event ID is not a v4 UUID: %q", envelope.EventID)
	}
	var data RideRequested
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if data.RideID != "ride-1" {
		t.Fatalf("RideID = %q, want ride-1", data.RideID)
	}
}
