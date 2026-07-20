package loadbalancer

import "time"

// HealthChecker periodically probes each backend and updates its health
// flag. Like ratelimit's sweeper, it has an explicit stop/done lifecycle
// — every HealthChecker started with StartHealthChecker must eventually
// have Close called, or its goroutine leaks for the life of the process.
type HealthChecker struct {
	stop chan struct{}
	done chan struct{}
}

// StartHealthChecker runs probe against every backend every interval,
// calling Backend.SetHealthy with the result.
func StartHealthChecker(backends []*Backend, interval time.Duration, probe func(*Backend) bool) *HealthChecker {
	hc := &HealthChecker{stop: make(chan struct{}), done: make(chan struct{})}
	go hc.run(backends, interval, probe)
	return hc
}

func (hc *HealthChecker) run(backends []*Backend, interval time.Duration, probe func(*Backend) bool) {
	defer close(hc.done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			for _, b := range backends {
				b.SetHealthy(probe(b))
			}
		case <-hc.stop:
			return
		}
	}
}

func (hc *HealthChecker) Close() {
	close(hc.stop)
	<-hc.done
}
