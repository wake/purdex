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

// Valid notations the line editor has to follow: spaced and quoted headers, multi-line arrays and strings, CRLF.
func TestSet_FollowsSpacedQuotedHeadersAndMultilineValues(t *testing.T) {
	for name, c := range map[string]struct{ text, key, val, want string }{
		"spaced header":   {"[ gate . slots ]\nheavy = 1\n", "slots.heavy", "2", "[ gate . slots ]\nheavy = 2\n"},
		"quoted header":   {"[\"gate\".\"slots\"]\nheavy = 1\nlight = 3\n", "slots.heavy", "2", "[\"gate\".\"slots\"]\nheavy = 2\nlight = 3\n"},
		"multiline array": {"[gate.intercept]\ntools = [\n  \"go\", # keep?\n  \"pnpm\",\n]\nmod_rewrite = true\n", "intercept.tools", `["go"]`, "[gate.intercept]\ntools = [\"go\"]\nmod_rewrite = true\n"},
		"array in value":  {"[gate.intercept]\ntools = [\"a]\", \"b\"]\nmod_rewrite = true\n", "intercept.tools", `["go"]`, "[gate.intercept]\ntools = [\"go\"]\nmod_rewrite = true\n"},
		"after other":     {"[gate]\nenabled = true\n[[other]]\nx = 1\n[gate.slots]\nheavy = 1\n", "slots.heavy", "2", "[gate]\nenabled = true\n[[other]]\nx = 1\n[gate.slots]\nheavy = 2\n"},
	} {
		t.Run(name, func(t *testing.T) {
			p := cfgFile(t, c.text)
			_, err := Set(p, "", c.key, c.val)
			require.NoError(t, err)
			assert.Equal(t, c.want, read(t, p))
		})
	}
}

// The record carries the profile in force when the command started, not the one it queued under.
func TestRun_TheRecordedProfileIsTheOneAfterTheWait(t *testing.T) {
	g, _ := testGate(t)
	g.SetConfig(func(c *Config) { c.Slots.Heavy = 1 })
	hold, release := holdSlot(t, g, ClassHeavy, "a")
	eventually(t, "the first command to hold the slot", func() bool { return len(g.Status().Heavy) == 1 })
	waiter := runAsync(g, context.Background(), Options{Argv: sh("true"), Class: ClassHeavy})
	eventually(t, "the second to queue", func() bool { return g.Status().WaitHeavy >= 1 })
	g.SetConfig(func(c *Config) { c.Profile = "night" })
	release()
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

// status spends a bounded time on the lock-holder lookups, both pools together, whatever one lookup takes.
func TestStatus_TheLockLookupsHaveOneBudget(t *testing.T) {
	g, _ := testGate(t)
	g.SetConfig(func(c *Config) { c.Slots.Heavy, c.Slots.Light = 2, 2 })
	old := statusLookupBudget
	statusLookupBudget = 150 * time.Millisecond
	defer func() { statusLookupBudget = old }()
	var slow atomic.Bool
	g.Holders = func(string) []Straggler {
		if slow.Load() {
			time.Sleep(3 * time.Second)
			return []Straggler{{PID: 1}}
		}
		return nil
	}
	var holds []<-chan runResult
	var releases []func()
	for i, c := range []Class{ClassHeavy, ClassHeavy, ClassLight, ClassLight} {
		h, rel := holdSlot(t, g, c, string(rune('a'+i)))
		holds, releases = append(holds, h), append(releases, rel)
	}
	eventually(t, "four held slots", func() bool { return len(g.Status().Heavy) == 2 && len(g.Status().Light) == 2 })
	slow.Store(true)
	begin := time.Now()
	st := g.Status()
	assert.Less(t, time.Since(begin), time.Second)
	assert.Empty(t, st.Heavy[0].Locked, "an answer that came too late is not shown")
	slow.Store(false)
	for _, rel := range releases {
		rel()
	}
	for _, h := range holds {
		<-h
	}
}
