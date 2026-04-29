package monitor

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTmuxPaneListerListsActiveAndInactivePanes(t *testing.T) {
	runner := &fakeTmuxCommandRunner{output: "$1\twork\t%1\t101\n$1\twork\t%2\t202\n$2\tlogs\t%3\t303\n"}
	lister := NewTmuxPaneLister(runner)

	panes, err := lister.ListPanes(context.Background())

	require.NoError(t, err)
	assert.Equal(t, [][]string{{"list-panes", "-a", "-F", tmuxPaneListFormat}}, runner.calls)
	assert.Equal(t, []TmuxPane{
		{TmuxSessionID: "$1", TmuxSessionName: "work", PaneID: "%1", PanePID: 101},
		{TmuxSessionID: "$1", TmuxSessionName: "work", PaneID: "%2", PanePID: 202},
		{TmuxSessionID: "$2", TmuxSessionName: "logs", PaneID: "%3", PanePID: 303},
	}, panes)
}

func TestTmuxPaneListerReturnsEmptyForEmptyOutput(t *testing.T) {
	lister := NewTmuxPaneLister(&fakeTmuxCommandRunner{output: "\n\n"})

	panes, err := lister.ListPanes(context.Background())

	require.NoError(t, err)
	assert.Empty(t, panes)
}

func TestTmuxPaneListerSkipsBlankLines(t *testing.T) {
	lister := NewTmuxPaneLister(&fakeTmuxCommandRunner{output: "\n$1\twork\t%1\t101\n\n"})

	panes, err := lister.ListPanes(context.Background())

	require.NoError(t, err)
	assert.Equal(t, []TmuxPane{{TmuxSessionID: "$1", TmuxSessionName: "work", PaneID: "%1", PanePID: 101}}, panes)
}

func TestTmuxPaneListerReturnsErrorForMalformedLine(t *testing.T) {
	lister := NewTmuxPaneLister(&fakeTmuxCommandRunner{output: "$1\twork\t%1\n"})

	panes, err := lister.ListPanes(context.Background())

	assert.Nil(t, panes)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "malformed tmux pane line 1")
}

func TestTmuxPaneListerReturnsErrorForInvalidPanePID(t *testing.T) {
	for _, rawPID := range []string{"nope", "0", "-1"} {
		t.Run(rawPID, func(t *testing.T) {
			lister := NewTmuxPaneLister(&fakeTmuxCommandRunner{output: "$1\twork\t%1\t" + rawPID + "\n"})

			panes, err := lister.ListPanes(context.Background())

			assert.Nil(t, panes)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "invalid tmux pane pid on line 1")
		})
	}
}

func TestTmuxPaneListerReturnsEmptyForNoServerOrSessions(t *testing.T) {
	for _, output := range []string{"no server running on /tmp/tmux", "no sessions"} {
		t.Run(output, func(t *testing.T) {
			lister := NewTmuxPaneLister(&fakeTmuxCommandRunner{output: output, err: errors.New("tmux failed")})

			panes, err := lister.ListPanes(context.Background())

			require.NoError(t, err)
			assert.Empty(t, panes)
		})
	}
}

func TestTmuxPaneListerWrapsRunnerError(t *testing.T) {
	lister := NewTmuxPaneLister(&fakeTmuxCommandRunner{err: errors.New("boom")})

	panes, err := lister.ListPanes(context.Background())

	assert.Nil(t, panes)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tmux list-panes")
}

func TestProcessModelHasExplicitUnits(t *testing.T) {
	processType := reflect.TypeOf(Process{})

	assertProcessField(t, processType, "PID", reflect.Int, `json:"pid"`)
	assertProcessField(t, processType, "PPID", reflect.Int, `json:"ppid"`)
	assertProcessField(t, processType, "Command", reflect.String, `json:"command"`)
	assertProcessField(t, processType, "CPUPercent", reflect.Float64, `json:"cpu_percent"`)
	assertProcessField(t, processType, "MemoryBytes", reflect.Uint64, `json:"memory_bytes"`)
}

func TestProcessCollectorListsProcessTableWithExplicitUnits(t *testing.T) {
	collector := NewProcessTableCollector(&fakeProcessCommandRunner{output: "  PID  PPID  %CPU RSS COMMAND\n 101 1 0.0 1024 tmux\n 201 101 12.5 2048 node   server.js\n 202 101 1.5 512\n"})

	processes, err := collector.ListProcesses(context.Background())

	require.NoError(t, err)
	assert.Equal(t, [][]string{{"-axo", "pid=,ppid=,pcpu=,rss=,command="}}, collector.(*processTableCollector).runner.(*fakeProcessCommandRunner).calls)
	assert.Equal(t, []Process{
		{PID: 101, PPID: 1, Command: "tmux", CPUPercent: 0, MemoryBytes: 1024 * 1024},
		{PID: 201, PPID: 101, Command: "node   server.js", CPUPercent: 12.5, MemoryBytes: 2048 * 1024},
		{PID: 202, PPID: 101, Command: "", CPUPercent: 1.5, MemoryBytes: 512 * 1024},
	}, processes)
}

func TestProcessCollectorRejectsInvalidNumericValues(t *testing.T) {
	for _, tc := range []struct {
		name    string
		line    string
		message string
	}{
		{name: "zero pid", line: "0 1 1.0 100 bad", message: "invalid process pid"},
		{name: "negative pid", line: "-1 1 1.0 100 bad", message: "invalid process pid"},
		{name: "negative ppid", line: "10 -1 1.0 100 bad", message: "invalid process ppid"},
		{name: "negative cpu", line: "10 1 -1.0 100 bad", message: "invalid process cpu percent"},
		{name: "nan cpu", line: "10 1 NaN 100 bad", message: "invalid process cpu percent"},
		{name: "inf cpu", line: "10 1 +Inf 100 bad", message: "invalid process cpu percent"},
		{name: "rss overflow", line: "10 1 1.0 18014398509481984 bad", message: "invalid process rss"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			collector := NewProcessTableCollector(&fakeProcessCommandRunner{output: tc.line + "\n"})

			processes, err := collector.ListProcesses(context.Background())

			assert.Nil(t, processes)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.message)
		})
	}
}

func TestAggregatePaneDescendantsAggregatesDirectChildren(t *testing.T) {
	got := AggregatePaneDescendants(101, []Process{
		{PID: 101, PPID: 1, Command: "shell", CPUPercent: 1, MemoryBytes: 100},
		{PID: 201, PPID: 101, Command: "node", CPUPercent: 12.5, MemoryBytes: 2000},
		{PID: 202, PPID: 101, Command: "go", CPUPercent: 3.5, MemoryBytes: 3000},
	})

	assert.Equal(t, PaneProcessAggregate{CPUPercent: 17, MemoryBytes: 5100, ProcessCount: 3}, got)
}

func TestAggregatePaneDescendantsExcludesUnrelatedProcesses(t *testing.T) {
	got := AggregatePaneDescendants(101, []Process{
		{PID: 101, PPID: 1, Command: "shell", CPUPercent: 1, MemoryBytes: 100},
		{PID: 201, PPID: 101, Command: "node", CPUPercent: 2, MemoryBytes: 200},
		{PID: 999, PPID: 1, Command: "other", CPUPercent: 50, MemoryBytes: 5000},
		{PID: 1000, PPID: 999, Command: "other-child", CPUPercent: 20, MemoryBytes: 1000},
	})

	assert.Equal(t, PaneProcessAggregate{CPUPercent: 3, MemoryBytes: 300, ProcessCount: 2}, got)
}

func TestAggregatePaneDescendantsIncludesNestedDescendants(t *testing.T) {
	got := AggregatePaneDescendants(101, []Process{
		{PID: 101, PPID: 1, Command: "shell", CPUPercent: 1, MemoryBytes: 100},
		{PID: 201, PPID: 101, Command: "npm", CPUPercent: 2, MemoryBytes: 200},
		{PID: 301, PPID: 201, Command: "vite", CPUPercent: 3, MemoryBytes: 300},
		{PID: 401, PPID: 301, Command: "esbuild", CPUPercent: 4, MemoryBytes: 400},
	})

	assert.Equal(t, PaneProcessAggregate{CPUPercent: 10, MemoryBytes: 1000, ProcessCount: 4}, got)
}

func TestAggregatePaneDescendantsHandlesCyclesAndMalformedParents(t *testing.T) {
	got := AggregatePaneDescendants(101, []Process{
		{PID: 101, PPID: 303, Command: "shell", CPUPercent: 1, MemoryBytes: 100},
		{PID: 201, PPID: 101, Command: "worker", CPUPercent: 2, MemoryBytes: 200},
		{PID: 202, PPID: 202, Command: "self-cycle", CPUPercent: 50, MemoryBytes: 5000},
		{PID: 303, PPID: 201, Command: "cycle", CPUPercent: 3, MemoryBytes: 300},
		{PID: 404, PPID: -1, Command: "malformed", CPUPercent: 80, MemoryBytes: 8000},
		{PID: 0, PPID: 101, Command: "bad-pid", CPUPercent: 90, MemoryBytes: 9000},
	})

	assert.Equal(t, 6.0, got.CPUPercent)
	assert.Equal(t, uint64(600), got.MemoryBytes)
	assert.Equal(t, 3, got.ProcessCount)
}

func TestAggregateSessionProcessesAggregatesAllPanesAndDeduplicatesByPID(t *testing.T) {
	got := AggregateSessionProcesses("$1", []TmuxPane{
		{TmuxSessionID: "$1", TmuxSessionName: "work", PaneID: "%1", PanePID: 101},
		{TmuxSessionID: "$1", TmuxSessionName: "work", PaneID: "%2", PanePID: 201},
		{TmuxSessionID: "$2", TmuxSessionName: "other", PaneID: "%3", PanePID: 901},
	}, []Process{
		{PID: 101, PPID: 1, Command: "shell", CPUPercent: 1, MemoryBytes: 100},
		{PID: 201, PPID: 101, Command: "worker", CPUPercent: 2, MemoryBytes: 200},
		{PID: 301, PPID: 201, Command: "nested", CPUPercent: 3, MemoryBytes: 300},
		{PID: 901, PPID: 1, Command: "unrelated", CPUPercent: 90, MemoryBytes: 9000},
	}, 10)

	assert.Equal(t, 6.0, got.CPUPercent)
	assert.Equal(t, uint64(600), got.MemoryBytes)
	assert.Equal(t, 3, got.ProcessCount)
}

func TestAggregateSessionProcessesReturnsBoundedTopProcesses(t *testing.T) {
	got := AggregateSessionProcesses("$1", []TmuxPane{
		{TmuxSessionID: "$1", TmuxSessionName: "work", PaneID: "%1", PanePID: 101},
	}, []Process{
		{PID: 101, PPID: 1, Command: "shell", CPUPercent: 1, MemoryBytes: 100},
		{PID: 201, PPID: 101, Command: "high-memory", CPUPercent: 5, MemoryBytes: 900},
		{PID: 202, PPID: 101, Command: "low-pid", CPUPercent: 5, MemoryBytes: 900},
		{PID: 203, PPID: 101, Command: "high-cpu", CPUPercent: 9, MemoryBytes: 10},
		{PID: 204, PPID: 101, Command: "hidden", CPUPercent: 4, MemoryBytes: 1000},
	}, 3)

	assert.Equal(t, 24.0, got.CPUPercent)
	assert.Equal(t, uint64(2910), got.MemoryBytes)
	assert.Equal(t, 5, got.ProcessCount)
	assert.Equal(t, []Process{
		{PID: 203, PPID: 101, Command: "high-cpu", CPUPercent: 9, MemoryBytes: 10},
		{PID: 201, PPID: 101, Command: "high-memory", CPUPercent: 5, MemoryBytes: 900},
		{PID: 202, PPID: 101, Command: "low-pid", CPUPercent: 5, MemoryBytes: 900},
	}, got.TopProcesses)
}

func TestAggregateSessionProcessesSortsTopProcessesByMemoryBeforePID(t *testing.T) {
	got := AggregateSessionProcesses("$1", []TmuxPane{
		{TmuxSessionID: "$1", TmuxSessionName: "work", PaneID: "%1", PanePID: 101},
	}, []Process{
		{PID: 101, PPID: 1, Command: "shell", CPUPercent: 1, MemoryBytes: 100},
		{PID: 201, PPID: 101, Command: "higher-memory", CPUPercent: 5, MemoryBytes: 900},
		{PID: 200, PPID: 101, Command: "lower-pid", CPUPercent: 5, MemoryBytes: 100},
	}, 2)

	assert.Equal(t, []Process{
		{PID: 201, PPID: 101, Command: "higher-memory", CPUPercent: 5, MemoryBytes: 900},
		{PID: 200, PPID: 101, Command: "lower-pid", CPUPercent: 5, MemoryBytes: 100},
	}, got.TopProcesses)
}

func assertProcessField(t *testing.T, processType reflect.Type, name string, kind reflect.Kind, tag string) {
	t.Helper()

	field, ok := processType.FieldByName(name)
	require.True(t, ok, "Process.%s must exist", name)
	assert.Equal(t, kind, field.Type.Kind())
	assert.Equal(t, reflect.StructTag(tag), field.Tag)
}

type fakeTmuxCommandRunner struct {
	output string
	err    error
	calls  [][]string
}

type fakeProcessCommandRunner struct {
	output string
	err    error
	calls  [][]string
}

func (r *fakeProcessCommandRunner) Output(_ context.Context, args ...string) (string, error) {
	r.calls = append(r.calls, args)
	return r.output, r.err
}

func (r *fakeTmuxCommandRunner) Output(_ context.Context, args ...string) (string, error) {
	r.calls = append(r.calls, args)
	return r.output, r.err
}
