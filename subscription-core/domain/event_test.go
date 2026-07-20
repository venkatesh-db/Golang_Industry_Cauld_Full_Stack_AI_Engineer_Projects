package domain

import "testing"

func TestEvent_TargetStatus(t *testing.T) {
	cases := []struct {
		name string
		ev   Event
		want Status
	}{
		{"canceled", Event{Type: EventSubscriptionCanceled}, StatusCanceled},
		{"payment failed -> past_due grace", Event{Type: EventPaymentFailed}, StatusPastDue},
		{"payment succeeded -> active", Event{Type: EventPaymentSucceeded}, StatusActive},
		{
			"created carries snapshot status",
			Event{Type: EventSubscriptionCreated, Subscription: Subscription{Status: StatusTrialing}},
			StatusTrialing,
		},
		{
			"updated with empty snapshot defaults active",
			Event{Type: EventSubscriptionUpdated, Subscription: Subscription{Status: ""}},
			StatusActive,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.ev.TargetStatus(); got != c.want {
				t.Errorf("TargetStatus() = %s, want %s", got, c.want)
			}
		})
	}
}
