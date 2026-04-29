package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHost_FirstCPUSampleIsPendingWhileMemoryAndDiskArePresent(t *testing.T) {
	collector := newFakeHostCollector()
	collector.cpuSamples = []HostCPUSample{{Idle: 80, Total: 100}}
	collector.memory = HostMemorySample{TotalBytes: 1000, UsedBytes: 250}
	collector.disk = HostDiskSample{TotalBytes: 2000, UsedBytes: 500}
	state := NewHostMetricsState(collector)

	host := collectHostMetrics(context.Background(), state)

	require.NotNil(t, host.CPU)
	assert.Nil(t, host.CPU.Percent)
	require.NotNil(t, host.CPU.UnavailableReason)
	assert.Equal(t, "pending", *host.CPU.UnavailableReason)
	require.NotNil(t, host.Memory)
	assert.Equal(t, uint64(1000), *host.Memory.TotalBytes)
	assert.Equal(t, uint64(250), *host.Memory.UsedBytes)
	assert.Equal(t, 25.0, *host.Memory.UsedPercent)
	require.NotNil(t, host.Disk)
	assert.Equal(t, uint64(2000), *host.Disk.TotalBytes)
	assert.Equal(t, uint64(500), *host.Disk.UsedBytes)
	assert.Equal(t, 25.0, *host.Disk.UsedPercent)
}

func TestHost_SecondCPUSampleComputesPercentFromDeltas(t *testing.T) {
	collector := newFakeHostCollector()
	collector.cpuSamples = []HostCPUSample{
		{Idle: 80, Total: 100},
		{Idle: 90, Total: 150},
	}
	state := NewHostMetricsState(collector)

	first := collectHostMetrics(context.Background(), state)
	second := collectHostMetrics(context.Background(), state)

	require.Nil(t, first.CPU.Percent)
	require.NotNil(t, second.CPU.Percent)
	assert.Equal(t, 80.0, *second.CPU.Percent)
	assert.Nil(t, second.CPU.UnavailableReason)
}

func TestHost_MemoryCollectorReturnsBytesAndPercent(t *testing.T) {
	collector := newFakeHostCollector()
	collector.memory = HostMemorySample{TotalBytes: 4096, UsedBytes: 1024}
	state := NewHostMetricsState(collector)

	host := collectHostMetrics(context.Background(), state)

	require.NotNil(t, host.Memory)
	assert.Equal(t, uint64(4096), *host.Memory.TotalBytes)
	assert.Equal(t, uint64(1024), *host.Memory.UsedBytes)
	assert.Equal(t, 25.0, *host.Memory.UsedPercent)
}

func TestHost_DiskCollectorReturnsSingleWholeHostSummary(t *testing.T) {
	collector := newFakeHostCollector()
	collector.disk = HostDiskSample{TotalBytes: 9000, UsedBytes: 3000}
	state := NewHostMetricsState(collector)

	host := collectHostMetrics(context.Background(), state)

	require.NotNil(t, host.Disk)
	assert.Equal(t, uint64(9000), *host.Disk.TotalBytes)
	assert.Equal(t, uint64(3000), *host.Disk.UsedBytes)
	assert.Equal(t, 33.33333333333333, *host.Disk.UsedPercent)
}

func TestHost_PartialCollectorFailureReturnsStableUnavailableReasons(t *testing.T) {
	collector := newFakeHostCollector()
	collector.cpuErr = errors.New("boom")
	collector.memoryErr = errors.New("boom")
	collector.diskErr = errors.New("boom")
	state := NewHostMetricsState(collector)

	host := collectHostMetrics(context.Background(), state)

	require.NotNil(t, host.CPU)
	assert.Nil(t, host.CPU.Percent)
	require.NotNil(t, host.CPU.UnavailableReason)
	assert.Equal(t, "host_cpu_unavailable", *host.CPU.UnavailableReason)
	require.NotNil(t, host.Memory)
	require.NotNil(t, host.Memory.UnavailableReason)
	assert.Equal(t, "host_memory_unavailable", *host.Memory.UnavailableReason)
	assert.Nil(t, host.Memory.TotalBytes)
	assert.Nil(t, host.Memory.UsedBytes)
	assert.Nil(t, host.Memory.UsedPercent)
	require.NotNil(t, host.Disk)
	require.NotNil(t, host.Disk.UnavailableReason)
	assert.Equal(t, "host_disk_unavailable", *host.Disk.UnavailableReason)
	assert.Nil(t, host.Disk.TotalBytes)
	assert.Nil(t, host.Disk.UsedBytes)
	assert.Nil(t, host.Disk.UsedPercent)
}

func TestHost_PartialCollectorFailureSerializesUnavailableValuesAsNull(t *testing.T) {
	collector := newFakeHostCollector()
	collector.memoryErr = errors.New("boom")
	collector.diskErr = errors.New("boom")
	state := NewHostMetricsState(collector)

	host := collectHostMetrics(context.Background(), state)
	data, err := json.Marshal(host)
	require.NoError(t, err)

	assert.Contains(t, string(data), `"memory":{"total_bytes":null,"used_bytes":null,"used_percent":null,"unavailable_reason":"host_memory_unavailable"}`)
	assert.Contains(t, string(data), `"disk":{"total_bytes":null,"used_bytes":null,"used_percent":null,"unavailable_reason":"host_disk_unavailable"}`)
}

func TestHost_PartialCollectorFailureKeepsAvailableMetrics(t *testing.T) {
	collector := newFakeHostCollector()
	collector.cpuErr = errors.New("boom")
	collector.memory = HostMemorySample{TotalBytes: 1000, UsedBytes: 400}
	collector.diskErr = errors.New("boom")
	state := NewHostMetricsState(collector)

	host := collectHostMetrics(context.Background(), state)

	require.NotNil(t, host.CPU)
	require.NotNil(t, host.CPU.UnavailableReason)
	assert.Equal(t, "host_cpu_unavailable", *host.CPU.UnavailableReason)
	require.NotNil(t, host.Memory)
	assert.Equal(t, uint64(1000), *host.Memory.TotalBytes)
	assert.Equal(t, uint64(400), *host.Memory.UsedBytes)
	assert.Equal(t, 40.0, *host.Memory.UsedPercent)
	assert.Nil(t, host.Memory.UnavailableReason)
	require.NotNil(t, host.Disk)
	require.NotNil(t, host.Disk.UnavailableReason)
	assert.Equal(t, "host_disk_unavailable", *host.Disk.UnavailableReason)
}

type fakeHostCollector struct {
	cpuSamples []HostCPUSample
	cpuCalls   int
	cpuErr     error

	memory      HostMemorySample
	memoryCalls int
	memoryErr   error

	disk      HostDiskSample
	diskCalls int
	diskErr   error
}

func newFakeHostCollector() *fakeHostCollector {
	return &fakeHostCollector{
		memory: HostMemorySample{TotalBytes: 1, UsedBytes: 0},
		disk:   HostDiskSample{TotalBytes: 1, UsedBytes: 0},
	}
}

func (c *fakeHostCollector) CollectCPU(context.Context) (HostCPUSample, error) {
	c.cpuCalls++
	if c.cpuErr != nil {
		return HostCPUSample{}, c.cpuErr
	}
	idx := c.cpuCalls - 1
	if idx >= len(c.cpuSamples) {
		return HostCPUSample{}, nil
	}
	sample := c.cpuSamples[idx]
	return sample, nil
}

func (c *fakeHostCollector) CollectMemory(context.Context) (HostMemorySample, error) {
	c.memoryCalls++
	if c.memoryErr != nil {
		return HostMemorySample{}, c.memoryErr
	}
	return c.memory, nil
}

func (c *fakeHostCollector) CollectDisk(context.Context) (HostDiskSample, error) {
	c.diskCalls++
	if c.diskErr != nil {
		return HostDiskSample{}, c.diskErr
	}
	return c.disk, nil
}
