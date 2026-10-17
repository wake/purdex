package gate

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- X2b-1: capability, the order of the hosts, the one ssh that decides whether to send ----

func TestAssistHost_TakesByTheCommandsWords(t *testing.T) {
	h := AssistHost{Can: []string{"go test", "vitest", "tsc -b"}, Cannot: []string{"vitest --ui", "go test ./cmd/pdx/ -run Free"}}
	assert.True(t, h.Takes([]string{"go", "test", "./..."}))
	assert.True(t, h.Takes([]string{"pnpm", "exec", "vitest", "run", "a.test.ts"}), "the runner words are skipped")
	assert.True(t, h.Takes([]string{"/repo/node_modules/.bin/tsc", "-b", "--force"}))
	assert.False(t, h.Takes([]string{"tsc", "--force", "-b"}), "a prefix, not a subset")
	assert.False(t, h.Takes([]string{"vitest", "--ui"}), "Cannot wins")
	assert.False(t, h.Takes([]string{"make", "lint"}), "not in Can")
	assert.False(t, AssistHost{}.Takes([]string{"go", "test"}), "no Can takes nothing")
}

func TestPickAssist_TheOrderAndWhoIsLeftOut(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	all := map[string]AssistHost{
		"mserv": {Can: []string{"phpunit", "go test"}},
		"air26": {Can: []string{"go test", "vitest"}},
		"mini":  {Can: []string{"vitest"}},
	}
	in := PickInput{Hosts: all, Argv: []string{"go", "test", "./..."}, Class: ClassHeavy, Repo: "purdex", Now: now}

	assert.Equal(t, []string{"air26", "mserv"}, PickAssist(dir, in), "by alias; only the hosts that take it")
	in.Argv = []string{"phpunit"}
	assert.Equal(t, []string{"mserv"}, PickAssist(dir, in), "a command only one host takes")
	in.Argv = []string{"make", "lint"}
	assert.Empty(t, PickAssist(dir, in), "no capability: stays on mlab")

	in.Argv = []string{"go", "test"}
	require.NoError(t, HoldHost(dir, "air26", "purdex", time.Minute, "busy", now))
	assert.Equal(t, []string{"mserv"}, PickAssist(dir, in), "a held host is left out")
	in.Repo = "other"
	assert.Equal(t, []string{"air26", "mserv"}, PickAssist(dir, in), "the hold is per repo")
}

func TestPickAssist_LightGoesOutOnlyWhenItWouldWaitOrMlabIsReserved(t *testing.T) {
	dir := t.TempDir()
	hosts := map[string]AssistHost{"air26": {Can: []string{"vitest"}}}
	in := PickInput{Hosts: hosts, Argv: []string{"vitest", "related"}, Class: ClassLight, Repo: "purdex", Now: time.Now()}
	assert.Empty(t, PickAssist(dir, in), "a light ticket mlab can run now stays")
	in.WouldWait = true
	assert.Equal(t, []string{"air26"}, PickAssist(dir, in))
	in.WouldWait, in.Reserved = false, true
	assert.Equal(t, []string{"air26"}, PickAssist(dir, in), "reserved: whatever can go, goes")
	in.Class, in.Reserved = ClassHeavy, false
	assert.Equal(t, []string{"air26"}, PickAssist(dir, in), "heavy goes with a free candidate")
}

func probeOn(t *testing.T, h *assistHost, host AssistHost) AssistProbe {
	t.Helper()
	g, _ := testGate(t)
	return g.ProbeAssist(context.Background(), AssistRun{Host: "air26", SSH: h.ssh}, host, []string{"go", "test", "./..."})
}

func TestProbeAssist_SendsWhenTheHostIsFreeAndIdle(t *testing.T) {
	h := newAssistHost(t, "purdex")
	p := probeOn(t, h, AssistHost{})
	assert.True(t, p.OK, p.Why)
}

func TestProbeAssist_TheHostsOwnGateWouldMakeItWait(t *testing.T) {
	h := newAssistHost(t, "purdex")
	writeFile(t, h.remote("fake-wouldwait"), `{"would_wait":true,"reason":"slots","class":"heavy","ahead":1}`, 0o644)
	p := probeOn(t, h, AssistHost{})
	assert.False(t, p.OK)
	assert.Contains(t, p.Why, "would make the command wait")
	assert.Zero(t, p.Hold, "not held: it asks again at the next look")
}

func TestProbeAssist_TheLimitsHoldTheHost(t *testing.T) {
	cases := map[string]struct {
		load, idle string
		host       AssistHost
		held       time.Duration
	}{
		"load1 over":        {"{ 12.00 2.00 2.00 }", "90.0", AssistHost{}, 5 * time.Minute},
		"load5 over":        {"{ 2.00 11.00 2.00 }", "90.0", AssistHost{}, 5 * time.Minute},
		"idle under":        {"{ 2.00 2.00 2.00 }", "10.0", AssistHost{}, 5 * time.Minute},
		"own limits":        {"{ 5.00 5.00 5.00 }", "90.0", AssistHost{LoadLimit: 0.4, HoldMin: 9}, 9 * time.Minute},
		"idle limit raised": {"{ 1.00 1.00 1.00 }", "50.0", AssistHost{IdleMin: 60}, 5 * time.Minute},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newAssistHost(t, "purdex")
			writeFile(t, h.remote("fake-loadavg"), c.load, 0o644)
			writeFile(t, h.remote("fake-idle"), c.idle, 0o644)
			p := probeOn(t, h, c.host)
			assert.False(t, p.OK)
			assert.Equal(t, c.held, p.Hold)
			assert.Contains(t, p.Why, "busy")
		})
	}
	// all under: sent (load1 is under but a limit of 1.0 per core allows 10)
	h := newAssistHost(t, "purdex")
	writeFile(t, h.remote("fake-loadavg"), "{ 9.50 9.50 9.50 }", 0o644)
	assert.True(t, probeOn(t, h, AssistHost{}).OK)
}

func TestProbeAssist_ASshFailureHoldsTheHostForTheUsualTime(t *testing.T) {
	h := newAssistHost(t, "purdex")
	t.Setenv("FAKE_SSH_FAIL", "1")
	p := probeOn(t, h, AssistHost{})
	assert.False(t, p.OK)
	assert.Equal(t, holdAfterFailure, p.Hold)
}

func TestProbeAssist_AnAnswerItCannotReadIsNotASend(t *testing.T) {
	h := newAssistHost(t, "purdex")
	writeFile(t, h.remote("fake-wouldwait"), `garbage`, 0o644)
	p := probeOn(t, h, AssistHost{})
	assert.False(t, p.OK)
	assert.Equal(t, holdAfterFailure, p.Hold)
}
