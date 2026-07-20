// Package telemetry provides bounded-label Prometheus instrumentation for Go
// HTTP services and their downstream dependencies.
//
// It intentionally requires canonical route and dependency allowlists. This
// avoids a common production outage where unbounded values such as user IDs,
// raw URLs, request IDs, or error text become Prometheus label values.
package telemetry
