package monitor

import "context"

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
	UnavailableReason string   `json:"unavailable_reason,omitempty"`
}

type HostMemoryMetrics struct {
	TotalBytes        *uint64  `json:"total_bytes"`
	UsedBytes         *uint64  `json:"used_bytes"`
	UsedPercent       *float64 `json:"used_percent"`
	UnavailableReason string   `json:"unavailable_reason,omitempty"`
}

type HostDiskMetrics struct {
	TotalBytes        *uint64  `json:"total_bytes"`
	UsedBytes         *uint64  `json:"used_bytes"`
	UsedPercent       *float64 `json:"used_percent"`
	UnavailableReason string   `json:"unavailable_reason,omitempty"`
}

type HostMetricsState struct {
	collector HostCollector
	previous  *HostCPUSample
}

func NewHostMetricsState(collector HostCollector) *HostMetricsState {
	return &HostMetricsState{collector: collector}
}

func collectHostMetrics(ctx context.Context, state *HostMetricsState) HostMetrics {
	if state == nil || state.collector == nil {
		return HostMetrics{
			CPU:    &HostCPUMetrics{UnavailableReason: hostCPUUnavailableReason},
			Memory: &HostMemoryMetrics{UnavailableReason: hostMemoryUnavailableReason},
			Disk:   &HostDiskMetrics{UnavailableReason: hostDiskUnavailableReason},
		}
	}

	return HostMetrics{
		CPU:    collectHostCPU(ctx, state),
		Memory: collectHostMemory(ctx, state.collector),
		Disk:   collectHostDisk(ctx, state.collector),
	}
}

func collectHostCPU(ctx context.Context, state *HostMetricsState) *HostCPUMetrics {
	sample, err := state.collector.CollectCPU(ctx)
	if err != nil {
		return &HostCPUMetrics{UnavailableReason: hostCPUUnavailableReason}
	}

	if state.previous == nil {
		state.previous = &sample
		return &HostCPUMetrics{UnavailableReason: hostCPUPendingReason}
	}

	percent := hostCPUPercent(*state.previous, sample)
	state.previous = &sample
	if percent == nil {
		return &HostCPUMetrics{UnavailableReason: hostCPUPendingReason}
	}
	return &HostCPUMetrics{Percent: percent}
}

func collectHostMemory(ctx context.Context, collector HostCollector) *HostMemoryMetrics {
	sample, err := collector.CollectMemory(ctx)
	if err != nil {
		return &HostMemoryMetrics{UnavailableReason: hostMemoryUnavailableReason}
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
		return &HostDiskMetrics{UnavailableReason: hostDiskUnavailableReason}
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
