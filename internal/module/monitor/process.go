package monitor

import (
	"context"
	"fmt"
	"math"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

type Process struct {
	PID         int     `json:"pid"`
	PPID        int     `json:"ppid"`
	Command     string  `json:"command"`
	CPUPercent  float64 `json:"cpu_percent"`
	MemoryBytes uint64  `json:"memory_bytes"`
}

type PaneProcessAggregate struct {
	CPUPercent   float64   `json:"cpu_percent"`
	MemoryBytes  uint64    `json:"memory_bytes"`
	ProcessCount int       `json:"process_count"`
	TopProcesses []Process `json:"top_processes"`
}

type ProcessCommandRunner interface {
	Output(ctx context.Context, args ...string) (string, error)
}

type processTableCollector struct {
	runner ProcessCommandRunner
}

func NewProcessTableCollector(runner ProcessCommandRunner) ProcessTableCollector {
	if runner == nil {
		runner = processCLICommandRunner{}
	}
	return &processTableCollector{runner: runner}
}

func (c *processTableCollector) ListProcesses(ctx context.Context) ([]Process, error) {
	out, err := c.runner.Output(ctx, "-axo", "pid=,ppid=,pcpu=,rss=,command=")
	if err != nil {
		return nil, fmt.Errorf("ps process table: %w", err)
	}
	return parseProcessTableOutput(out)
}

func parseProcessTableOutput(out string) ([]Process, error) {
	var processes []Process
	for i, line := range strings.Split(out, "\n") {
		lineNo := i + 1
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		fields, command := splitProcessTableLine(line)
		if len(fields) < 4 {
			return nil, fmt.Errorf("malformed process table line %d: expected at least 4 fields, got %d", lineNo, len(fields))
		}
		if strings.EqualFold(fields[0], "PID") {
			continue
		}

		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid <= 0 {
			return nil, fmt.Errorf("invalid process pid on line %d: %q", lineNo, fields[0])
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil || ppid < 0 {
			return nil, fmt.Errorf("invalid process ppid on line %d: %q", lineNo, fields[1])
		}
		cpuPercent, err := strconv.ParseFloat(fields[2], 64)
		if err != nil || math.IsNaN(cpuPercent) || math.IsInf(cpuPercent, 0) || cpuPercent < 0 {
			return nil, fmt.Errorf("invalid process cpu percent on line %d: %q", lineNo, fields[2])
		}
		rssKB, err := strconv.ParseUint(fields[3], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid process rss on line %d: %q", lineNo, fields[3])
		}
		if rssKB > math.MaxUint64/1024 {
			return nil, fmt.Errorf("invalid process rss on line %d: %q", lineNo, fields[3])
		}

		processes = append(processes, Process{
			PID:         pid,
			PPID:        ppid,
			CPUPercent:  cpuPercent,
			MemoryBytes: rssKB * 1024,
			Command:     command,
		})
	}
	return processes, nil
}

func splitProcessTableLine(line string) ([]string, string) {
	fields := make([]string, 0, 4)
	pos := 0
	for len(fields) < 4 {
		for pos < len(line) && (line[pos] == ' ' || line[pos] == '\t') {
			pos++
		}
		if pos >= len(line) {
			break
		}
		start := pos
		for pos < len(line) && line[pos] != ' ' && line[pos] != '\t' {
			pos++
		}
		fields = append(fields, line[start:pos])
	}
	if len(fields) < 4 {
		return fields, ""
	}
	for pos < len(line) && (line[pos] == ' ' || line[pos] == '\t') {
		pos++
	}
	if pos >= len(line) {
		return fields, ""
	}
	return fields, line[pos:]
}

func AggregatePaneDescendants(panePID int, processes []Process) PaneProcessAggregate {
	if panePID <= 0 {
		return PaneProcessAggregate{}
	}

	processByPID := make(map[int]Process, len(processes))
	childrenByPPID := make(map[int][]int, len(processes))
	for _, process := range processes {
		if process.PID <= 0 {
			continue
		}
		if _, exists := processByPID[process.PID]; !exists {
			processByPID[process.PID] = process
		}
		if process.PPID > 0 {
			childrenByPPID[process.PPID] = append(childrenByPPID[process.PPID], process.PID)
		}
	}

	var aggregate PaneProcessAggregate
	queue := []int{panePID}
	visited := make(map[int]bool, len(processes))
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if visited[pid] {
			continue
		}
		visited[pid] = true

		process, ok := processByPID[pid]
		if !ok {
			continue
		}
		aggregate.CPUPercent += process.CPUPercent
		aggregate.MemoryBytes += process.MemoryBytes
		aggregate.ProcessCount++
		queue = append(queue, childrenByPPID[pid]...)
	}

	return aggregate
}

func AggregateSessionProcesses(tmuxSessionID string, panes []TmuxPane, processes []Process, topLimit int) PaneProcessAggregate {
	if tmuxSessionID == "" {
		return PaneProcessAggregate{}
	}

	processByPID := make(map[int]Process, len(processes))
	childrenByPPID := make(map[int][]int, len(processes))
	for _, process := range processes {
		if process.PID <= 0 {
			continue
		}
		if _, exists := processByPID[process.PID]; !exists {
			processByPID[process.PID] = process
		}
		if process.PPID > 0 {
			childrenByPPID[process.PPID] = append(childrenByPPID[process.PPID], process.PID)
		}
	}

	var queue []int
	for _, pane := range panes {
		if pane.TmuxSessionID == tmuxSessionID && pane.PanePID > 0 {
			queue = append(queue, pane.PanePID)
		}
	}

	var aggregate PaneProcessAggregate
	var included []Process
	visited := make(map[int]bool, len(processes))
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if visited[pid] {
			continue
		}
		visited[pid] = true

		process, ok := processByPID[pid]
		if !ok {
			continue
		}
		aggregate.CPUPercent += process.CPUPercent
		aggregate.MemoryBytes += process.MemoryBytes
		aggregate.ProcessCount++
		included = append(included, process)
		queue = append(queue, childrenByPPID[pid]...)
	}
	aggregate.TopProcesses = selectTopProcesses(included, topLimit)

	return aggregate
}

func selectTopProcesses(processes []Process, limit int) []Process {
	if limit <= 0 || len(processes) == 0 {
		return nil
	}
	sorted := append([]Process(nil), processes...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].CPUPercent != sorted[j].CPUPercent {
			return sorted[i].CPUPercent > sorted[j].CPUPercent
		}
		if sorted[i].MemoryBytes != sorted[j].MemoryBytes {
			return sorted[i].MemoryBytes > sorted[j].MemoryBytes
		}
		return sorted[i].PID < sorted[j].PID
	})
	if len(sorted) > limit {
		sorted = sorted[:limit]
	}
	return sorted
}

type processCLICommandRunner struct{}

func (processCLICommandRunner) Output(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "ps", args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
