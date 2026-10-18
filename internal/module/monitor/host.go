package monitor

import (
	"context"
	"errors"
	"sync/atomic"
	"time"
)

const (
	hostCPUUnavailableReason    = "host_cpu_unavailable"
	hostMemoryUnavailableReason = "host_memory_unavailable"
	hostDiskUnavailableReason   = "host_disk_unavailable"
	hostCPUPendingReason        = "pending"
)

type HostCollector interface {
	CollectCPU(context.Context) (HostCPUSample, error)
	CollectMemory(context.Context) (HostMemorySample, error)
	CollectDisk(context.Context) (HostDiskSample, error)
}

type HostCPUSample struct {
	Idle  uint64
	Total uint64
}

type HostMemorySample struct {
	TotalBytes uint64
	UsedBytes  uint64
}

type HostDiskSample struct {
	TotalBytes uint64
	UsedBytes  uint64
}

type HostMetrics struct {
	CPU    *HostCPUMetrics    `json:"cpu"`
	Memory *HostMemoryMetrics `json:"memory"`
	Disk   *HostDiskMetrics   `json:"disk"`
}

type HostCPUMetrics struct {
	Percent           *float64 `json:"percent"`
	UnavailableReason *string  `json:"unavailable_reason"`
}

type HostMemoryMetrics struct {
	TotalBytes        *uint64  `json:"total_bytes"`
	UsedBytes         *uint64  `json:"used_bytes"`
	UsedPercent       *float64 `json:"used_percent"`
	UnavailableReason *string  `json:"unavailable_reason"`
}

type HostDiskMetrics struct {
	TotalBytes        *uint64  `json:"total_bytes"`
	UsedBytes         *uint64  `json:"used_bytes"`
	UsedPercent       *float64 `json:"used_percent"`
	UnavailableReason *string  `json:"unavailable_reason"`
}

// hostCPUPercentSource is a collector that reports the CPU utilisation itself instead of counters to take deltas of
// (darwin, #2013). The interval is how often the caller wants it refreshed.
type hostCPUPercentSource interface {
	CPUPercent(interval time.Duration) (float64, error)
}

type HostMetricsState struct {
	collector HostCollector
	previous  *HostCPUSample
	// cpuInterval is the monitor's refresh interval in nanoseconds, the pace of a background CPU sampler. Atomic: the
	// snapshot that sets it and the one that reads it are not ordered by anything this type owns.
	cpuInterval atomic.Int64
}

func (s *HostMetricsState) setCPUInterval(d time.Duration) { s.cpuInterval.Store(int64(d)) }

func NewHostMetricsState(collector HostCollector) *HostMetricsState {
	return &HostMetricsState{collector: collector}
}

func collectHostMetrics(ctx context.Context, state *HostMetricsState) HostMetrics {
	if state == nil || state.collector == nil {
		return HostMetrics{
			CPU:    &HostCPUMetrics{UnavailableReason: reasonPtr(hostCPUUnavailableReason)},
			Memory: &HostMemoryMetrics{UnavailableReason: reasonPtr(hostMemoryUnavailableReason)},
			Disk:   &HostDiskMetrics{UnavailableReason: reasonPtr(hostDiskUnavailableReason)},
		}
	}

	// One snapshot has one source: a collector that can choose per call (the pool-backed one) is pinned to its choice for all three.
	col := state.collector
	if p, ok := col.(interface{ pinned() HostCollector }); ok {
		col = p.pinned()
	}
	return HostMetrics{
		CPU:    collectHostCPUFrom(ctx, state, col),
		Memory: collectHostMemory(ctx, col),
		Disk:   collectHostDisk(ctx, col),
	}
}

func collectHostCPU(ctx context.Context, state *HostMetricsState) *HostCPUMetrics {
	return collectHostCPUFrom(ctx, state, state.collector)
}

// collectHostCPUFrom reads the CPU from col, which is the state's collector or the view of it pinned for one snapshot.
func collectHostCPUFrom(ctx context.Context, state *HostMetricsState, col HostCollector) *HostCPUMetrics {
	if pc, ok := col.(interface{ poolCPU() (float64, bool) }); ok { // wwd's reading, when the pool is the source
		if percent, ok := pc.poolCPU(); ok {
			state.previous = nil // the delta baseline of the own collector is old by the time it is needed again: start it afresh
			return &HostCPUMetrics{Percent: &percent}
		}
	}
	if src, ok := col.(hostCPUPercentSource); ok {
		interval := time.Duration(state.cpuInterval.Load())
		if interval <= 0 {
			interval = DefaultRefreshIntervalMS * time.Millisecond
		}
		percent, err := src.CPUPercent(interval)
		switch {
		case errors.Is(err, errCPUPending):
			return &HostCPUMetrics{UnavailableReason: reasonPtr(hostCPUPendingReason)}
		case err != nil:
			return &HostCPUMetrics{UnavailableReason: reasonPtr(hostCPUUnavailableReason)}
		}
		return &HostCPUMetrics{Percent: &percent}
	}
	sample, err := col.CollectCPU(ctx)
	if err != nil {
		return &HostCPUMetrics{UnavailableReason: reasonPtr(hostCPUUnavailableReason)}
	}

	if state.previous == nil {
		state.previous = &sample
		return &HostCPUMetrics{UnavailableReason: reasonPtr(hostCPUPendingReason)}
	}

	percent := hostCPUPercent(*state.previous, sample)
	state.previous = &sample
	if percent == nil {
		return &HostCPUMetrics{UnavailableReason: reasonPtr(hostCPUPendingReason)}
	}
	return &HostCPUMetrics{Percent: percent}
}

func collectHostMemory(ctx context.Context, collector HostCollector) *HostMemoryMetrics {
	sample, err := collector.CollectMemory(ctx)
	if err != nil {
		return &HostMemoryMetrics{UnavailableReason: reasonPtr(hostMemoryUnavailableReason)}
	}
	usedPercent := usedPercent(sample.UsedBytes, sample.TotalBytes)
	return &HostMemoryMetrics{
		TotalBytes:  &sample.TotalBytes,
		UsedBytes:   &sample.UsedBytes,
		UsedPercent: &usedPercent,
	}
}

func collectHostDisk(ctx context.Context, collector HostCollector) *HostDiskMetrics {
	sample, err := collector.CollectDisk(ctx)
	if err != nil {
		return &HostDiskMetrics{UnavailableReason: reasonPtr(hostDiskUnavailableReason)}
	}
	usedPercent := usedPercent(sample.UsedBytes, sample.TotalBytes)
	return &HostDiskMetrics{
		TotalBytes:  &sample.TotalBytes,
		UsedBytes:   &sample.UsedBytes,
		UsedPercent: &usedPercent,
	}
}

func hostCPUPercent(previous, current HostCPUSample) *float64 {
	if current.Total <= previous.Total || current.Idle < previous.Idle {
		return nil
	}

	deltaTotal := current.Total - previous.Total
	deltaIdle := current.Idle - previous.Idle
	if deltaTotal == 0 || deltaIdle > deltaTotal {
		return nil
	}

	percent := float64(deltaTotal-deltaIdle) / float64(deltaTotal) * 100
	return &percent
}

func usedPercent(used, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return float64(used) / float64(total) * 100
}
