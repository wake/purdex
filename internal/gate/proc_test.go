package gate

import (
	"errors"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAncestors(t *testing.T) {
	tab := map[int]procInfo{50: {PPID: 40}, 40: {PPID: 30}, 30: {PPID: 1}, 1: {PPID: 0}, 60: {PPID: 61}, 61: {PPID: 60}}
	assert.Equal(t, []int{40, 30}, ancestors(tab, 50), "up to, not including, init")
	assert.Empty(t, ancestors(tab, 30))
	assert.Equal(t, []int{61}, ancestors(tab, 60), "a loop stops at once")
	assert.Empty(t, ancestors(tab, 999), "unknown pid")
}

func TestDescendants(t *testing.T) {
	tab := map[int]procInfo{10: {PPID: 1, Start: 1}, 11: {PPID: 10, Start: 2}, 12: {PPID: 11, Start: 3}, 13: {PPID: 10, Start: 4}, 20: {PPID: 1, Start: 5}}
	assert.Equal(t, map[int]int64{11: 2, 12: 3, 13: 4}, descendants(tab, 10))
	assert.Empty(t, descendants(tab, 20))
}

// Once the command is gone, a process that got its pid must not bring its own children into the tree.
// (M: scan without looking at the root.)
func TestTree_APidThatWasReusedAsTheRootIsNotScanned(t *testing.T) {
	tab := map[int]procInfo{100: {PPID: 1, Start: 5}, 200: {PPID: 100, Start: 6}}
	tr := newTree(100, func() (map[int]procInfo, error) { return tab, nil })
	tr.scan()
	assert.Equal(t, map[int]int64{200: 6}, tr.seen, "the command's own child")

	tab = map[int]procInfo{100: {PPID: 1, Start: 99}, 300: {PPID: 100, Start: 100}} // the root exited; another process has pid 100
	tr.scan()
	assert.NotContains(t, tr.seen, 300, "a stranger's child is not ours")

	tab = map[int]procInfo{300: {PPID: 1, Start: 100}} // the root is gone altogether
	tr.scan()
	assert.NotContains(t, tr.seen, 300)
}

// The pid is looked at again just before the signal: a remembered process that is gone, or whose pid is another
// process now, is not signalled. (M: signal without the second look.)
func TestTree_NoSignalToAPidThatIsSomeoneElseNow(t *testing.T) {
	tab := map[int]procInfo{100: {PPID: 1, Start: 5}, 200: {PPID: 100, Start: 6}, 201: {PPID: 100, Start: 7}}
	var sent []int
	tr := newTree(100, func() (map[int]procInfo, error) { return tab, nil })
	tr.kill = func(pid int, sig syscall.Signal) error { sent = append(sent, pid); return nil }
	tr.scan()
	require.Len(t, tr.seen, 2)

	tab = map[int]procInfo{200: {PPID: 1, Start: 6}, 201: {PPID: 1, Start: 777}} // 200 is still itself; 201's pid is reused
	tr.signal(200, syscall.SIGTERM)
	tr.signal(201, syscall.SIGTERM)
	tr.signal(999, syscall.SIGTERM) // never seen
	assert.Equal(t, []int{200}, sent)
}

// A real tree: a child of the command that outlives it is found by the walk and ended.
func TestTree_ReapEndsWhatTheWalkSaw(t *testing.T) {
	cmd := exec.Command("sh", "-c", "sleep 300 & echo $! ; wait")
	out, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	buf := make([]byte, 32)
	n, _ := out.Read(buf)
	var pid int
	for _, c := range buf[:n] {
		if c >= '0' && c <= '9' {
			pid = pid*10 + int(c-'0')
		}
	}
	require.NotZero(t, pid)
	tr := newTree(cmd.Process.Pid, procTable)
	tr.scan()
	require.Contains(t, tr.seen, pid)
	require.NoError(t, cmd.Process.Kill()) // the command dies, the sleep is adopted by init
	_ = cmd.Wait()
	tr.reap(300 * time.Millisecond)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && syscall.Kill(pid, 0) == nil {
		time.Sleep(10 * time.Millisecond)
	}
	assert.ErrorIs(t, syscall.Kill(pid, 0), syscall.ESRCH, "the leftover is gone")
}

// If the process table cannot be read at the start, the tree cannot tell the command from a stranger that gets its pid
// later, so it follows nothing and signals nothing. (M: learn the root at a later look.)
func TestTree_AFailedFirstLookFollowsNothing(t *testing.T) {
	calls := 0
	tab := map[int]procInfo{100: {PPID: 1, Start: 5}, 200: {PPID: 100, Start: 6}}
	var sent []int
	procs := func() (map[int]procInfo, error) {
		calls++
		if calls <= 3 { // newTree tries three times
			return nil, errors.New("sysctl failed")
		}
		return tab, nil
	}
	tr := newTree(100, procs)
	tr.kill = func(pid int, sig syscall.Signal) error { sent = append(sent, pid); return nil }
	assert.False(t, tr.known())
	tr.scan()
	assert.Empty(t, tr.seen, "the root's identity is unknown: nothing is followed")
	// the root exits, its pid is reused by a stranger with children
	tab = map[int]procInfo{100: {PPID: 1, Start: 99}, 300: {PPID: 100, Start: 100}}
	tr.scan()
	tr.reap(10 * time.Millisecond)
	assert.Empty(t, tr.seen)
	assert.Empty(t, sent, "and nothing is signalled")
}

// One failed look is not the end: the start tries again.
func TestTree_TheStartTriesThreeTimes(t *testing.T) {
	calls := 0
	procs := func() (map[int]procInfo, error) {
		calls++
		if calls < 3 {
			return nil, errors.New("sysctl failed")
		}
		return map[int]procInfo{100: {PPID: 1, Start: 5}, 200: {PPID: 100, Start: 6}}, nil
	}
	tr := newTree(100, procs)
	assert.True(t, tr.known())
	tr.scan()
	assert.Equal(t, map[int]int64{200: 6}, tr.seen)
}
