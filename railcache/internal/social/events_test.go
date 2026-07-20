package social

import (
	"encoding/json"
	"testing"
)

func TestEventEnvelopeRoundTrip(t *testing.T) {
	event, err := NewEvent(EventEngagementRecorded, "post_ocean", "request-7", LikeRecorded{
		LikeID: "like-1", PostID: "post_ocean", PostAuthorID: "maya", ActorID: "niko",
	})
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}
	raw, err := event.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	decoded, err := DecodeEvent(raw)
	if err != nil {
		t.Fatalf("DecodeEvent: %v", err)
	}
	if decoded.ID != event.ID || decoded.Key != "post_ocean" || decoded.CorrelationID != "request-7" {
		t.Fatalf("envelope changed during round trip: %#v", decoded)
	}
	var payload LikeRecorded
	if err := json.Unmarshal(decoded.Data, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.ActorID != "niko" || payload.PostAuthorID != "maya" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestDecodeEventRejectsMissingContractFields(t *testing.T) {
	if _, err := DecodeEvent([]byte(`{"id":"only-an-id"}`)); err == nil {
		t.Fatal("DecodeEvent accepted an incomplete envelope")
	}
}
