package monitor

import (
	"context"
	"errors"
	"time"
)

var errCPUPending = errors.New("host cpu: no sample yet")

type cpuSampler struct {
	run       func(context.Context) (string, error)
	logf      func(string, ...any)
	idleAfter time.Duration
	timeout   time.Duration
}

func newCPUSampler(run func(context.Context) (string, error), logf func(string, ...any)) *cpuSampler {
	return &cpuSampler{run: run, logf: logf}
}

func (s *cpuSampler) CPUPercent(time.Duration) (float64, error) { return 0, errors.New("red stub") }

func (s *cpuSampler) Close() {}

func parseIostatCPU(string) (float64, error) { return 0, errors.New("red stub") }
