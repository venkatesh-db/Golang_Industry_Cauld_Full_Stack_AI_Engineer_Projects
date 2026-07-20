package concurrency

import (
	"fmt"
	"math"
	"testing"
)

func TestHLLAccuracy(t *testing.T) {
	for _, n := range []int{100, 10_000, 200_000} {
		e := NewEstimator()
		for i := 0; i < n; i++ {
			e.Add(fmt.Sprintf("account-%d", i))
		}
		est := e.Estimate()
		relErr := math.Abs(est-float64(n)) / float64(n)
		if relErr > 0.03 { // guardrail: within ±3% (target ~2%)
			t.Fatalf("n=%d estimate=%.0f relErr=%.4f exceeds 3%%", n, est, relErr)
		}
	}
}

func TestHLLMerge(t *testing.T) {
	a, b := NewEstimator(), NewEstimator()
	for i := 0; i < 5000; i++ {
		a.Add(fmt.Sprintf("x-%d", i))
	}
	for i := 2500; i < 7500; i++ { // 2500 overlap -> union 7500
		b.Add(fmt.Sprintf("x-%d", i))
	}
	a.Merge(b)
	est := a.Estimate()
	if relErr := math.Abs(est-7500) / 7500; relErr > 0.04 {
		t.Fatalf("merged estimate=%.0f relErr=%.4f", est, relErr)
	}
}
