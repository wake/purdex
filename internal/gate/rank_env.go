package gate

// PDX_GATE_RANK=back: a gate started with it queues its ticket at rank back from the moment the ticket is visible (spec §5.2).
// Only back is taken from the environment: a variable anyone can set must not let a command jump the queue (front) or stall it
// (hold). Used by assist runs sent from another host, so the person's own development on this host is served first.

import (
	"os"
	"strings"
	"unicode/utf8"
)

// RankEnv and RankReasonEnv are the variables the gate reads.
const (
	RankEnv       = "PDX_GATE_RANK"
	RankReasonEnv = "PDX_GATE_RANK_REASON"
)

// BornRank is the rank a ticket is made with.
type BornRank struct {
	Rank       Rank
	By, Reason string
}

const maxBornReason = 200

// bornRank reads the rank variables of the run's environment (the process's own when the options carry none). Anything but "back"
// is ignored with one warning line.
func (g *Gate) bornRank(o Options) *BornRank {
	env := o.Env
	if env == nil {
		env = os.Environ()
	}
	v := envValue(env, RankEnv)
	if v == "" {
		return nil
	}
	if v != string(RankBack) {
		g.say("PDX_GATE_RANK=%s ignored (only back)", v)
		return nil
	}
	by := g.Session
	if by == "" {
		by = "env"
	}
	reason := "PDX_GATE_RANK=back"
	if r := envValue(env, RankReasonEnv); validBornReason(r) {
		reason = r
	}
	return &BornRank{Rank: RankBack, By: by, Reason: reason}
}

func validBornReason(r string) bool {
	if r == "" || len(r) > maxBornReason || !utf8.ValidString(r) {
		return false
	}
	return !strings.ContainsFunc(r, func(c rune) bool { return c < 0x20 || c == 0x7f })
}
