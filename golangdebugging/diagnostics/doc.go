// Package diagnostics creates bounded, production-safe incident snapshots.
//
// An incident snapshot puts the signals needed to triage a Go service in one
// place: recent structured log records, runtime memory and scheduler health,
// and (when explicitly requested) an aggregated goroutine dump. It is designed
// for internal use only; the supplied HTTP handler requires an authorizer.
package diagnostics
