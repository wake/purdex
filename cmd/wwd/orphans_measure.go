package main

import (
	"fmt"
	"os"
	"sort"
	"syscall"
	"time"

	"github.com/wake/purdex/internal/wwd/facts"
	"github.com/wake/purdex/internal/wwd/orphans"
)

// cpuTime is the CPU (user + system) this process has used so far.
func cpuTime() time.Duration {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}

func median(ds []time.Duration) time.Duration {
	s := append([]time.Duration(nil), ds...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[len(s)/2]
}

// measureOrphans is `wwd orphans-scan --measure N`: what the scan costs on this host, from N runs over the same snapshot. The dev-path scan
// alone (no Fresh lookup, so no burst) is what runs on every 5 s sample; the full Observe adds the burst, which runs every orphans.CheckEvery.
// The snapshot itself is the sampler's and is not counted. Wall time is the clock; CPU time is this process's user + system time.
func measureOrphans(snap facts.ProcSnapshot, ctx facts.ProcContext, n int) {
	noBurst := ctx
	noBurst.Fresh = nil
	var devWall, devCPU, fullWall, fullCPU []time.Duration
	run := func(c facts.ProcContext, wall, cpu *[]time.Duration) {
		s := orphans.New(orphans.Config{UID: os.Getuid()}) // a new scanner: its first Observe always makes the loop check
		c0, t0 := cpuTime(), time.Now()
		s.Observe(t0, snap, c)
		*wall = append(*wall, time.Since(t0))
		*cpu = append(*cpu, cpuTime()-c0)
	}
	for i := 0; i < n; i++ {
		run(noBurst, &devWall, &devCPU)
		run(ctx, &fullWall, &fullCPU)
	}
	burstCPU := max(median(fullCPU)-median(devCPU), 0)
	burstWall := max(median(fullWall)-median(devWall), 0)
	fmt.Printf("measured over %d runs on %d processes (the snapshot itself is the sampler's and is not counted)\n", n, len(snap.Procs))
	fmt.Printf("  dev-path scan, every 5 s:  wall %s  cpu %s   (first run, argv not cached: wall %s cpu %s)\n",
		median(devWall).Round(10*time.Microsecond), median(devCPU).Round(10*time.Microsecond),
		devWall[0].Round(10*time.Microsecond), devCPU[0].Round(10*time.Microsecond))
	fmt.Printf("  loop burst, every %s:     wall %s  cpu %s\n", orphans.CheckEvery, burstWall.Round(time.Millisecond), burstCPU.Round(10*time.Microsecond))
	perMin := 12*median(devCPU) + time.Duration(float64(time.Minute)/float64(orphans.CheckEvery)*float64(burstCPU))
	fmt.Printf("  per minute: %d dev scans + %.0f bursts = cpu %s (%.3f%% of one core)\n",
		12, float64(time.Minute)/float64(orphans.CheckEvery), perMin.Round(10*time.Microsecond), 100*perMin.Seconds()/60)
}
