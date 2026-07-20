package domain

// Feature identifies a gated capability the app checks.
type Feature string

// Plan describes a tier and its per-feature limits.
// Limit semantics: -1 unlimited, 0 disabled, >0 a metered cap.
type Plan struct {
	ID           string
	Tier         string
	Features     map[Feature]int64
	SeatIncluded int
}

// Entitlement is the derived projection the app checks against — never raw
// provider fields scattered through call sites.
type Entitlement struct {
	SubscriptionID string
	Tier           string
	Features       map[Feature]int64
	Seats          int
	Active         bool
}

// DecisionReason explains a Check outcome.
type DecisionReason string

const (
	ReasonAllowed        DecisionReason = "allowed"
	ReasonNotActive      DecisionReason = "subscription_not_active"
	ReasonFeatureOff     DecisionReason = "feature_disabled"
	ReasonQuotaExceeded  DecisionReason = "quota_exceeded"
	ReasonUnknownFeature DecisionReason = "unknown_feature"
)

// Decision is the result of an entitlement check.
type Decision struct {
	Allow  bool
	Reason DecisionReason
	Limit  int64 // -1 unlimited, >=0 the applicable cap
}

// StatusGrantsAccess encodes the grace policy: past_due retains access;
// canceled and paused do not.
func StatusGrantsAccess(s Status) bool {
	switch s {
	case StatusActive, StatusTrialing, StatusPastDue:
		return true
	default:
		return false
	}
}

// DeriveEntitlement computes the entitlement projection from a subscription and
// its resolved plan.
func DeriveEntitlement(sub Subscription, plan Plan) Entitlement {
	seats := sub.SeatCount
	if plan.SeatIncluded > seats {
		seats = plan.SeatIncluded
	}
	return Entitlement{
		SubscriptionID: sub.ID,
		Tier:           plan.Tier,
		Features:       plan.Features,
		Seats:          seats,
		Active:         StatusGrantsAccess(sub.Status),
	}
}

// Evaluate returns the base decision for a feature, ignoring metered usage
// (the entitlements service layers usage on top).
func (e Entitlement) Evaluate(f Feature) Decision {
	if !e.Active {
		return Decision{Allow: false, Reason: ReasonNotActive}
	}
	limit, ok := e.Features[f]
	if !ok {
		return Decision{Allow: false, Reason: ReasonUnknownFeature}
	}
	if limit == 0 {
		return Decision{Allow: false, Reason: ReasonFeatureOff, Limit: 0}
	}
	return Decision{Allow: true, Reason: ReasonAllowed, Limit: limit}
}
