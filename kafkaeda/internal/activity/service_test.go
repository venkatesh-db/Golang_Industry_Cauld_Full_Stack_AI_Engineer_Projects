package activity

import (
	"testing"

	"github.com/venkatesh/kafkaeda/internal/events"
)

func TestDetailsForAssignment(t *testing.T) {
	envelope, err := events.New("ride.assigned", "dispatch-service", "correlation-1", "cause-1", events.RideAssigned{RideID: "ride-7", DriverName: "Aarav"})
	if err != nil {
		t.Fatal(err)
	}
	rideID, kind, title, err := details(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if rideID != "ride-7" || kind != "assigned" || title != "Captain Aarav is assigned" {
		t.Fatalf("details = (%q, %q, %q)", rideID, kind, title)
	}
}
