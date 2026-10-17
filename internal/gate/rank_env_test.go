package gate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- PDX_GATE_RANK=back: a ticket born at rank back (spec §5.2) ----

// An older back ticket is served after a newer normal one, and it shows by/reason in the queue.
// Mutation gate: drop the rank write -> A runs first (red).
func TestRankEnv_BackTicketYieldsToLaterNormal(t *testing.T) {
	g, _ := testGate(t)
	g.Session = "sess-assist"
	g.SetConfig(func(c *Config) { c.Slots.Heavy = 1 })
	release, holder := holderOf(t, g)
	logFile := filepath.Join(t.TempDir(), "order")
	a := runAsync(g, context.Background(), Options{Argv: logCommand(logFile, "A"), Class: ClassHeavy,
		Env: cleanEnv("PDX_GATE_RANK=back", "PDX_GATE_RANK_REASON=assist run")})
	queuedTickets(t, g, 1)
	b := runAsync(g, context.Background(), Options{Argv: logCommand(logFile, "B"), Class: ClassHeavy})
	ws := queuedTickets(t, g, 2)
	assert.Contains(t, strings.Join(ws[0].Argv, " "), "echo B", "the normal ticket is first in line")
	assert.Equal(t, RankBack, ws[1].Rank)
	assert.Equal(t, "sess-assist", ws[1].RankBy)
	assert.Equal(t, "assist run", ws[1].RankReason)
	release()
	<-holder
	assert.Equal(t, 0, (<-a).code)
	assert.Equal(t, 0, (<-b).code)
	got, err := os.ReadFile(logFile)
	require.NoError(t, err)
	assert.Equal(t, "B\nA\n", string(got))
}

func TestRankEnv_DefaultReasonAndBy(t *testing.T) {
	g, _ := testGate(t)
	g.Session = ""
	for _, r := range []string{"", "a\nb", "x\x01y", strings.Repeat("r", 201)} {
		env := []string{"PDX_GATE_RANK=back"}
		if r != "" {
			env = append(env, "PDX_GATE_RANK_REASON="+r)
		}
		b := g.bornRank(Options{Env: env})
		require.NotNil(t, b)
		assert.Equal(t, "PDX_GATE_RANK=back", b.Reason, "reason %q falls back", r)
		assert.Equal(t, "env", b.By)
	}
	b := g.bornRank(Options{Env: []string{"PDX_GATE_RANK=back", "PDX_GATE_RANK_REASON=" + strings.Repeat("r", 200)}})
	assert.Equal(t, strings.Repeat("r", 200), b.Reason)
}

// Mutation gate: accept front (or hold) from the environment (red).
func TestRankEnv_OnlyBackIsAccepted(t *testing.T) {
	for _, v := range []string{"front", "hold", "normal", "BACK", "garbage"} {
		g, buf := testGate(t)
		assert.Nil(t, g.bornRank(Options{Env: []string{"PDX_GATE_RANK=" + v}}), v)
		assert.Contains(t, buf.String(), "pdx gate: PDX_GATE_RANK="+v+" ignored (only back)\n")
		assert.Equal(t, 1, strings.Count(buf.String(), "ignored"))
	}
	g, buf := testGate(t)
	assert.Nil(t, g.bornRank(Options{Env: []string{"PATH=/bin"}}))
	assert.Empty(t, buf.String(), "no variable, no change, no line")
}

// No reader of the queue (a scan that cleans, a serve) ever finds a born-back ticket as normal: the rank and the rename go under
// one hold of the admit lock. Mutation gate: write the rank after the rename, outside the lock (red; many rounds).
func TestRankEnv_NeverVisibleAsNormal(t *testing.T) {
	p := &Pool{Dir: t.TempDir(), Class: ClassHeavy, Size: 1, Now: time.Now}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var bad []string
	var mu sync.Mutex
	note := func(s string) { mu.Lock(); bad = append(bad, s); mu.Unlock() }
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				for _, e := range p.peekSnapshot().Queue {
					if e.Rank.Rank != RankBack {
						note(e.Name + " " + string(e.Rank.Rank))
					}
				}
				if s, err := p.Snapshot(); err == nil {
					for _, e := range s.Queue {
						if e.Rank.Rank != RankBack {
							note(e.Name + " locked " + string(e.Rank.Rank))
						}
					}
				}
			}
		}()
	}
	var tickets []*Ticket
	for i := 0; i < 150; i++ {
		tk, err := p.EnqueueRanked(WaiterInfo{PID: os.Getpid(), Argv: []string{"x"}}, &BornRank{Rank: RankBack, By: "env", Reason: "r"})
		require.NoError(t, err)
		tickets = append(tickets, tk)
	}
	close(stop)
	wg.Wait()
	assert.Empty(t, bad)
	for _, tk := range tickets {
		tk.Drop()
	}
	left, _ := filepath.Glob(filepath.Join(p.queueDir(), "*"))
	assert.Empty(t, left, "no rank file outlives its ticket")
}
