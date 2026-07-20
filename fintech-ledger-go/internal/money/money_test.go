package money

import "testing"

// TestPaise_NoDrift proves the exact-integer property by summing one
// paise, ten million times, and checking the total is exact. The
// float64 sibling of this test (see the commented block below) drifts.
func TestPaise_NoDrift(t *testing.T) {
	var total Paise
	const n = 10_000_000
	for i := 0; i < n; i++ {
		total = total.Add(1)
	}
	if total != Paise(n) {
		t.Fatalf("got %d, want %d", total, n)
	}
}

// TestFloat64Drifts_Documentation shows the exact failure mode Paise
// avoids: summing 0.01 (one paise as rupees) n times with float64 does
// not equal n*0.01 once n is large enough for rounding error to compound.
func TestFloat64Drifts_Documentation(t *testing.T) {
	var total float64
	const n = 10_000_000
	for i := 0; i < n; i++ {
		total += 0.01
	}
	want := float64(n) * 0.01
	if total == want {
		t.Skip("float64 happened not to drift on this platform/n; the risk remains for other n")
	}
}

func TestString(t *testing.T) {
	cases := []struct {
		p    Paise
		want string
	}{
		{FromRupees(100, 50), "₹100.50"},
		{FromRupees(0, 5), "₹0.05"},
		{FromRupees(-100, -50), "-₹100.50"},
	}
	for _, c := range cases {
		if got := c.p.String(); got != c.want {
			t.Errorf("Paise(%d).String() = %q, want %q", c.p, got, c.want)
		}
	}
}
