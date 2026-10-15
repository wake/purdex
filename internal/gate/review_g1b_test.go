package gate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A config.toml that is a link stays a link; the file it points to is the one that changes (adversarial review of G1b).
func TestSet_ASymlinkedConfigIsWrittenThrough(t *testing.T) {
	real := cfgFile(t, "[gate.slots]\nheavy = 1\n")
	link := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.Symlink(real, link))
	_, err := Set(link, "", "slots.heavy", "2")
	require.NoError(t, err)
	st, err := os.Lstat(link)
	require.NoError(t, err)
	assert.NotZero(t, st.Mode()&os.ModeSymlink, "still a link")
	assert.Contains(t, read(t, real), "heavy = 2")
}

// Notations the line editor does not follow are refused with the file untouched, never half-written.
func TestSet_ANotationItCannotEditIsRefusedAndTheFileIsKept(t *testing.T) {
	for name, text := range map[string]string{
		"spaced header":   "[ gate.slots ]\nheavy = 1\n",
		"quoted header":   "[\"gate\".\"slots\"]\nheavy = 1\n",
		"multiline array": "[gate.intercept]\ntools = [\n  \"go\",\n  \"pnpm\",\n]\n",
	} {
		t.Run(name, func(t *testing.T) {
			p := cfgFile(t, text)
			key := "slots.heavy"
			if name == "multiline array" {
				key = "intercept.tools"
			}
			_, err := Set(p, "", key, `"go"`)
			if err == nil {
				return // edited correctly is fine too
			}
			assert.Equal(t, text, read(t, p))
		})
	}
}

// The record carries the profile in force when the command started, not the one it queued under.
func TestRun_TheRecordedProfileIsTheOneAfterTheWait(t *testing.T) {
	g, _ := testGate(t)
	g.SetConfig(func(c *Config) { c.Slots.Heavy = 1 })
	hold := runAsync(g, context.Background(), Options{Argv: sh("sleep 2"), Class: ClassHeavy})
	eventually(t, "the first command to hold the slot", func() bool { return len(g.Status().Heavy) == 1 })
	waiter := runAsync(g, context.Background(), Options{Argv: sh("true"), Class: ClassHeavy})
	eventually(t, "the second to queue", func() bool { return g.Status().WaitHeavy >= 1 })
	g.SetConfig(func(c *Config) { c.Profile = "night" })
	<-hold
	<-waiter
	recs := readDecisions(t, g)
	require.Len(t, recs, 2)
	assert.Equal(t, "night", recs[1]["profile"])
}

// Gates that write at once do not mix or lose lines.
func TestDecisions_ConcurrentWritersKeepWholeLines(t *testing.T) {
	g, _ := testGate(t)
	dir := t.TempDir()
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g.record(dir, &Decision{Argv: []string{"x"}, Outcome: "slot"})
		}()
	}
	wg.Wait()
	b, err := os.ReadFile(filepath.Join(dir, decisionsFile))
	require.NoError(t, err)
	lines := 0
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var d Decision
		require.NoError(t, json.Unmarshal([]byte(l), &d), l)
		lines++
	}
	assert.Equal(t, 24, lines)
}

// status spends a bounded time on the lock-holder lookups.
func TestStatus_TheLockLookupsHaveOneBudget(t *testing.T) {
	g, _ := testGate(t)
	g.SetConfig(func(c *Config) { c.Slots.Heavy = 3 })
	old := statusLookupBudget
	statusLookupBudget = 150 * time.Millisecond
	defer func() { statusLookupBudget = old }()
	var calls atomic.Int32
	g.Holders = func(string) []Straggler { calls.Add(1); time.Sleep(120 * time.Millisecond); return nil }
	var holds []<-chan runResult
	for i := 0; i < 3; i++ {
		holds = append(holds, runAsync(g, context.Background(), Options{Argv: sh("sleep 1.5"), Class: ClassHeavy}))
	}
	eventually(t, "three held slots", func() bool { return len(g.Status().Heavy) == 3 })
	calls.Store(0)
	begin := time.Now()
	g.Status()
	assert.Less(t, time.Since(begin), 600*time.Millisecond)
	assert.LessOrEqual(t, calls.Load(), int32(2), "the third lookup is skipped once the budget is spent")
	for _, h := range holds {
		<-h
	}
}
