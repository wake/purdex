package testcmd

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/wake/purdex/internal/wwd/wire"
)

// poolReader is what the submitter reads of the pool (*client.Client has it). An API without it skips the check.
type poolReader interface {
	Pool(ctx context.Context) (wire.Pool, error)
}

// checkPool judges a job's labels against the pool as the submitter sees it (#3546), before anything is queued. When no host holds all
// of the labels, that is a configuration problem that waiting never fixes: it prints which labels are missing and returns ExitNoHost
// (stop true). When some host holds them but none can take the job right now (worker off, unreachable, owner active), the wait may be
// short, so the job is queued as usual and one line says who it is waiting for. A pool that cannot be read judges nothing.
//
// A peer that does not answer counts with its last known labels, so a host that is only down is not taken for a missing setting; one
// never reached (no host id, no labels) holds nothing.
func checkPool(ctx context.Context, o Options, task string, labels []string) (code int, stop bool) {
	pr, ok := o.API.(poolReader)
	if !ok || len(labels) == 0 {
		return 0, false
	}
	pool, err := pr.Pool(ctx)
	if err != nil {
		return 0, false
	}
	var holders []wire.Peer
	settling := false
	var lacks []string // "alias: a, b" for each host that lacks some label
	have := map[string]bool{}
	for _, p := range pool.Peers {
		if !p.Reachable && p.Info.HostID == "" && len(p.Info.Labels) == 0 {
			continue // never reached: nothing is known of it
		}
		if p.Reachable && p.Facts == nil {
			settling = true // answers but has no facts record yet: its labels (repo:, host:, earned task:) are not all in
		}
		set := map[string]bool{}
		for _, l := range p.Info.Labels {
			set[l] = true
			have[l] = true
		}
		var miss []string
		for _, l := range labels {
			if !set[l] {
				miss = append(miss, l)
			}
		}
		if len(miss) == 0 {
			holders = append(holders, p)
		} else {
			lacks = append(lacks, fmt.Sprintf("%s lacks %s", p.Info.Alias, strings.Join(miss, ", ")))
		}
	}
	if len(holders) == 0 && settling {
		return 0, false // a host that has not finished announcing its labels may still hold them: queue as usual
	}
	if len(holders) == 0 {
		var none []string
		for _, l := range labels {
			if !have[l] {
				none = append(none, l)
			}
		}
		sort.Strings(lacks)
		msg := fmt.Sprintf("pdx test: no host of the pool holds all the labels of %s (%s)", task, strings.Join(labels, ", "))
		if len(none) > 0 {
			msg += "; no host has: " + strings.Join(none, ", ")
		}
		fmt.Fprintln(o.Stderr, msg)
		for _, l := range lacks {
			fmt.Fprintln(o.Stderr, "  "+l)
		}
		return ExitNoHost, true
	}
	var waits []string
	for _, p := range holders {
		why := notReadyReason(p)
		if why == "" {
			return 0, false // a holder can take it now
		}
		waits = append(waits, fmt.Sprintf("%s (%s)", p.Info.Alias, why))
	}
	fmt.Fprintf(o.Stderr, "pdx test: no host that holds the labels can take %s right now, queued anyway: %s\n", task, strings.Join(waits, ", "))
	return 0, false
}

// notReadyReason is why a peer that holds the labels cannot take a job now; "" when it can.
func notReadyReason(p wire.Peer) string {
	switch {
	case !p.Reachable:
		return "unreachable"
	case !p.Info.Worker.Enabled:
		return "worker off"
	case p.Facts != nil && p.Facts.Owner != nil && p.Facts.Owner.Active && p.Facts.Worker.OwnerRule != "":
		return "owner active, yielding"
	}
	return ""
}
