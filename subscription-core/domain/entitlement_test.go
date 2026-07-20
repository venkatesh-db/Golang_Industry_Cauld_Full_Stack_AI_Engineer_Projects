package domain

import "testing"

const (
	featAPI     Feature = "api_calls"
	featSeats   Feature = "seats"
	featExports Feature = "exports"
)

func proPlan() Plan {
	return Plan{
		ID:   "price_pro",
		Tier: "pro",
		Features: map[Feature]int64{
			featAPI:     10000, // metered cap
			featExports: -1,    // unlimited
			featSeats:   0,     // disabled on this plan
		},
		SeatIncluded: 3,
	}
}

func TestStatusGrantsAccess(t *testing.T) {
	grant := map[Status]bool{
		StatusActive:   true,
		StatusTrialing: true,
		StatusPastDue:  true, // grace period retains access
		StatusCanceled: false,
		StatusPaused:   false,
	}
	for s, want := range grant {
		if got := StatusGrantsAccess(s); got != want {
			t.Errorf("StatusGrantsAccess(%s) = %v, want %v", s, got, want)
		}
	}
}

func TestDeriveEntitlement_SeatFloorFromPlan(t *testing.T) {
	sub := Subscription{ID: "s1", Status: StatusActive, SeatCount: 1}
	ent := DeriveEntitlement(sub, proPlan())
	if ent.Seats != 3 {
		t.Errorf("seats = %d, want 3 (plan floor applies when sub seats lower)", ent.Seats)
	}

	sub.SeatCount = 5
	ent = DeriveEntitlement(sub, proPlan())
	if ent.Seats != 5 {
		t.Errorf("seats = %d, want 5 (sub seats above floor win)", ent.Seats)
	}
}

func TestEvaluate_Decisions(t *testing.T) {
	active := DeriveEntitlement(Subscription{Status: StatusActive}, proPlan())

	cases := []struct {
		name    string
		ent     Entitlement
		feature Feature
		want    Decision
	}{
		{"allowed metered", active, featAPI, Decision{Allow: true, Reason: ReasonAllowed, Limit: 10000}},
		{"allowed unlimited", active, featExports, Decision{Allow: true, Reason: ReasonAllowed, Limit: -1}},
		{"feature disabled", active, featSeats, Decision{Allow: false, Reason: ReasonFeatureOff, Limit: 0}},
		{"unknown feature", active, Feature("nope"), Decision{Allow: false, Reason: ReasonUnknownFeature}},
		{
			"inactive subscription",
			DeriveEntitlement(Subscription{Status: StatusCanceled}, proPlan()),
			featAPI,
			Decision{Allow: false, Reason: ReasonNotActive},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.ent.Evaluate(c.feature)
			if got != c.want {
				t.Errorf("Evaluate(%s) = %+v, want %+v", c.feature, got, c.want)
			}
		})
	}
}
