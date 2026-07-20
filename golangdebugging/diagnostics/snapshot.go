package diagnostics

import (
	"context"
	"errors"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	// ErrSnapshotInProgress prevents multiple callers from concurrently taking
	// an expensive all-goroutine dump.
	ErrSnapshotInProgress = errors.New("diagnostics: goroutine snapshot already in progress")
)

// Options controls the bounded work performed for a snapshot.
type Options struct {
	Events             *EventBuffer
	MaxEvents          int
	MaxStackBytes      int
	MaxGoroutineGroups int
	Now                func() time.Time
}

// Service creates correlated incident snapshots.
type Service struct {
	events             *EventBuffer
	maxEvents          int
	maxStackBytes      int
	maxGoroutineGroups int
	now                func() time.Time
	stackMu            sync.Mutex
}

// NewService constructs an incident snapshot service. Events may be nil when
// application log capture is not desired.
func NewService(options Options) *Service {
	if options.MaxEvents <= 0 {
		options.MaxEvents = 50
	}
	if options.MaxStackBytes <= 0 {
		options.MaxStackBytes = 1 << 20 // 1 MiB hard ceiling for a stack dump.
	}
	if options.MaxStackBytes < 4<<10 {
		options.MaxStackBytes = 4 << 10
	}
	if options.MaxGoroutineGroups <= 0 {
		options.MaxGoroutineGroups = 20
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Service{
		events:             options.Events,
		maxEvents:          options.MaxEvents,
		maxStackBytes:      options.MaxStackBytes,
		maxGoroutineGroups: options.MaxGoroutineGroups,
		now:                options.Now,
	}
}

// Snapshot contains a point-in-time collection of signals used for triage.
type Snapshot struct {
	CapturedAt time.Time        `json:"captured_at"`
	Runtime    RuntimeMetrics   `json:"runtime"`
	Events     []Event          `json:"events,omitempty"`
	Goroutines *GoroutineReport `json:"goroutines,omitempty"`
}

// RuntimeMetrics contains cheap, process-wide runtime health indicators.
// Values are gauges or cumulative totals; rate calculations belong in the
// monitoring backend where there is a time series.
type RuntimeMetrics struct {
	Goroutines      int     `json:"goroutines"`
	GOMAXPROCS      int     `json:"gomaxprocs"`
	CPUs            int     `json:"cpus"`
	HeapAllocBytes  uint64  `json:"heap_alloc_bytes"`
	HeapInUseBytes  uint64  `json:"heap_inuse_bytes"`
	HeapObjects     uint64  `json:"heap_objects"`
	NextGCBytes     uint64  `json:"next_gc_bytes"`
	TotalAllocBytes uint64  `json:"total_alloc_bytes"`
	GCCycles        uint32  `json:"gc_cycles"`
	GCPauseTotalNS  uint64  `json:"gc_pause_total_ns"`
	GCCPUFraction   float64 `json:"gc_cpu_fraction"`
}

// GoroutineReport is an aggregated, size-bounded goroutine dump. Identical
// goroutine stacks are grouped so contention patterns stand out quickly.
type GoroutineReport struct {
	Observed      int              `json:"observed"`
	Truncated     bool             `json:"truncated"`
	OmittedGroups int              `json:"omitted_groups,omitempty"`
	Groups        []GoroutineGroup `json:"groups"`
}

// GoroutineGroup is one unique goroutine stack and the number of goroutines
// currently in that state and stack.
type GoroutineGroup struct {
	State string `json:"state"`
	Count int    `json:"count"`
	Stack string `json:"stack"`
}

// Capture takes a lightweight snapshot, optionally including a bounded,
// all-goroutine dump. Stack collection is serialized to avoid amplifying a
// production incident with concurrent expensive runtime.Stack calls.
func (s *Service) Capture(ctx context.Context, includeGoroutines bool) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}

	snapshot := Snapshot{
		CapturedAt: s.now().UTC(),
		Runtime:    readRuntimeMetrics(),
	}
	if s.events != nil {
		snapshot.Events = s.events.Recent(s.maxEvents)
	}
	if !includeGoroutines {
		return snapshot, nil
	}
	if !s.stackMu.TryLock() {
		return Snapshot{}, ErrSnapshotInProgress
	}
	defer s.stackMu.Unlock()

	report := collectGoroutines(s.maxStackBytes, s.maxGoroutineGroups)
	snapshot.Goroutines = &report
	return snapshot, nil
}

func readRuntimeMetrics() RuntimeMetrics {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return RuntimeMetrics{
		Goroutines:      runtime.NumGoroutine(),
		GOMAXPROCS:      runtime.GOMAXPROCS(0),
		CPUs:            runtime.NumCPU(),
		HeapAllocBytes:  stats.HeapAlloc,
		HeapInUseBytes:  stats.HeapInuse,
		HeapObjects:     stats.HeapObjects,
		NextGCBytes:     stats.NextGC,
		TotalAllocBytes: stats.TotalAlloc,
		GCCycles:        stats.NumGC,
		GCPauseTotalNS:  stats.PauseTotalNs,
		GCCPUFraction:   stats.GCCPUFraction,
	}
}

func collectGoroutines(maxBytes, maxGroups int) GoroutineReport {
	stack, truncated := allGoroutineStacks(maxBytes)
	groups := groupGoroutines(string(stack))
	report := GoroutineReport{Truncated: truncated}
	for _, group := range groups {
		report.Observed += group.Count
	}
	if len(groups) > maxGroups {
		report.OmittedGroups = len(groups) - maxGroups
		groups = groups[:maxGroups]
	}
	report.Groups = groups
	return report
}

func allGoroutineStacks(maxBytes int) ([]byte, bool) {
	buffer := make([]byte, 4<<10)
	for len(buffer) < maxBytes {
		n := runtime.Stack(buffer, true)
		if n < len(buffer) {
			return buffer[:n], false
		}
		newSize := len(buffer) * 2
		if newSize > maxBytes {
			newSize = maxBytes
		}
		buffer = make([]byte, newSize)
	}
	n := runtime.Stack(buffer, true)
	if n < len(buffer) {
		return buffer[:n], false
	}
	return buffer, true
}

func groupGoroutines(dump string) []GoroutineGroup {
	byStack := make(map[string]*GoroutineGroup)
	for _, block := range strings.Split(strings.TrimSpace(dump), "\n\n") {
		lines := strings.Split(block, "\n")
		if len(lines) == 0 || !strings.HasPrefix(lines[0], "goroutine ") {
			continue
		}
		state, ok := goroutineState(lines[0])
		if !ok {
			continue
		}
		lines[0] = "goroutine [" + state + "]:"
		stack := strings.Join(lines, "\n")
		if group, exists := byStack[stack]; exists {
			group.Count++
			continue
		}
		byStack[stack] = &GoroutineGroup{State: state, Count: 1, Stack: stack}
	}
	groups := make([]GoroutineGroup, 0, len(byStack))
	for _, group := range byStack {
		groups = append(groups, *group)
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Count != groups[j].Count {
			return groups[i].Count > groups[j].Count
		}
		return groups[i].Stack < groups[j].Stack
	})
	return groups
}

func goroutineState(header string) (string, bool) {
	start := strings.IndexByte(header, '[')
	end := strings.IndexByte(header, ']')
	if start < 0 || end <= start+1 {
		return "", false
	}
	return header[start+1 : end], true
}

// BuildInfo returns build metadata suitable for an application health endpoint
// or log startup line. It is kept separate from Snapshot so callers can attach
// deployment information without repeating it each capture.
func BuildInfo() map[string]string {
	info := map[string]string{"go_version": runtime.Version()}
	if build, ok := debug.ReadBuildInfo(); ok {
		info["module"] = build.Main.Path
		info["version"] = build.Main.Version
		for _, setting := range build.Settings {
			if setting.Key == "vcs.revision" || setting.Key == "vcs.time" || setting.Key == "vcs.modified" {
				info[strings.TrimPrefix(setting.Key, "vcs.")] = setting.Value
			}
		}
	}
	return info
}
