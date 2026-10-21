package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/wake/purdex/internal/wwd/advisor"
	"github.com/wake/purdex/internal/wwd/daemon"
	"github.com/wake/purdex/internal/wwd/facts"
	"github.com/wake/purdex/internal/wwd/orphans"
	"github.com/wake/purdex/internal/wwd/queue"
	"github.com/wake/purdex/internal/wwd/store"
	"github.com/wake/purdex/internal/wwd/wire"
)

// queueReadTimeout bounds one read of the queue for the alert rules: a pass waits for it, and a rule that cannot read is left as it was.
const queueReadTimeout = 5 * time.Second

// advisorInputs is what the alert rules (spec §13.3) read on this host.
type advisorInputs struct {
	Alias    string
	WorkRoot string
	Store    *store.Store
	Broker   *daemon.Broker
	Sampler  *daemon.Sampler
	// Known is [advisor] known_residents: the residents whose large footprint is expected here.
	Known []string
	// Orphans is the latest process scan (internal/wwd/orphans); it returns nil before the first one.
	Orphans func() *advisor.OrphanReport
	// Peers are the peers of the pool and Downs their reachability (the silence rules); PartnerSeen is the partner check, nil when this host
	// has none ([advisor] partner_check).
	Peers       []daemon.PeerConfig
	Downs       func() []daemon.PeerDown
	PartnerSeen func() time.Time
	// Watch is the [advisor] section: which of the watcher's three pressure rules this host judges (watch_rules, off) and the swap growth
	// threshold. SampleSignal is signalled, without blocking, at every new sample: pressure danger is judged on it.
	Watch        daemon.AdvisorRule
	SampleSignal <-chan struct{}
	// Queue is this host's queue and Pool the pool as it sees it: what the queue rules (S3-3b) judge on. Either may be nil.
	Queue *queue.Queue
	Pool  func() []wire.Peer
}

// advisorRules are the rules this build judges (spec §13.3 except silence, thrash's broker hand-off and the incidents of 8b); the live
// evaluator and the replay run the same list.
func advisorRules(known []string, watch daemon.AdvisorRule) []advisor.Rule {
	rs := []advisor.Rule{
		advisor.DiskLow{}, advisor.DiskFalling{}, advisor.WorkerCrashLoop{},
		advisor.PressureHeld{}, advisor.Thrash{}, advisor.ResidentGrowth{Known: known}, advisor.MservService{}, advisor.Orphans{},
	}
	// the watcher's three pressure rules (S3-2) write a level and kinds an older reader refuses whole: only where [advisor] watch_rules is on
	if watch.WatchRuleOn(wire.AlertPressureWatch) {
		rs = append(rs, advisor.PressureWatch{})
	}
	if watch.WatchRuleOn(wire.AlertPressureDanger) {
		rs = append(rs, advisor.PressureDanger{})
	}
	if watch.WatchRuleOn(wire.AlertSwapGrowth) {
		rs = append(rs, advisor.SwapGrowth{ThresholdMB: watch.SwapGrowthMB})
	}
	// the queue's four rules (S3-3b), the same: only where [advisor] queue_rules is on
	if watch.QueueRules {
		rs = append(rs, advisor.QueueStalled{}, advisor.JobRequeued{}, advisor.QueueBacklog{}, &advisor.QueueUnclaimable{})
	}
	return rs
}

// queueKinds are the alert kinds of the queue's four rules.
var queueKinds = []wire.AlertKind{wire.AlertQueueStalled, wire.AlertJobRequeued, wire.AlertQueueBacklog, wire.AlertQueueUnclaimable}

// retiredQueueKeys are the alert keys of the queue rules when this host does not judge them: the evaluator clears them if they stand, so
// turning queue_rules off leaves no alert of the four kinds behind (docs/wwd-deploy.md §16).
func retiredQueueKeys(watch daemon.AdvisorRule) []string {
	if watch.QueueRules {
		return nil
	}
	out := make([]string, 0, len(queueKinds))
	for _, k := range queueKinds {
		out = append(out, string(k))
	}
	return out
}

// retiredWatchKeys are the alert keys of the watcher's rules this host does not judge: the evaluator clears them if they stand, so turning
// watch_rules off (or listing a rule in `off`) leaves no alert of the new kinds behind (docs/wwd-deploy.md §15).
func retiredWatchKeys(watch daemon.AdvisorRule) []string {
	var out []string
	for _, k := range []wire.AlertKind{wire.AlertPressureWatch, wire.AlertPressureDanger, wire.AlertSwapGrowth} {
		if !watch.WatchRuleOn(k) {
			out = append(out, string(k))
		}
	}
	return out
}

// silenceRules are the silence rules of spec §13.3 that this host runs: one per peer that has alert_unreachable on, and the partner check
// where the host has a Purdex daemon. They read the pool and the socket, which a replay of recorded facts does not have, so the replay does
// not run them.
func silenceRules(peers []daemon.PeerConfig, partner bool) []advisor.Rule {
	var rs []advisor.Rule
	for _, p := range peers {
		if p.AlertsUnreachable() {
			rs = append(rs, advisor.PeerSilence{Alias: p.Alias})
		}
	}
	if partner {
		rs = append(rs, advisor.PartnerDown{})
	}
	return rs
}

// runAdvisor judges the alert rules over this host's own facts until ctx ends. The first judgement waits one interval, so the first
// facts sample and the start record are in before a rule looks.
func runAdvisor(ctx context.Context, in advisorInputs) {
	book := daemon.NewAlertBook(in.Store, in.Alias, in.Broker, nil)
	input := liveInput(in)
	// the silence rules judge the pool and the partner, not the facts: they run on their own, so a host whose first facts sample never comes
	// (a sampler that keeps failing) still tells that a peer or the Purdex daemon is gone
	if rs := silenceRules(in.Peers, in.PartnerSeen != nil); len(rs) > 0 {
		go advisor.NewEvaluator(book, silenceInput(in), log.Printf, rs...).Run(ctx)
	}
	// not behind the proto 2 writer gate (spec §13.1): an unreachable host must not silence the others' alerts
	ev := advisor.NewEvaluator(book, input, log.Printf, advisorRules(in.Known, in.Watch)...).WithRetired(retiredWatchKeys(in.Watch)...).WithRetired(retiredQueueKeys(in.Watch)...)
	if in.SampleSignal != nil {
		ev = ev.WithSampleSignal(in.SampleSignal) // the same goroutine judges the 30 s pass and the per-sample pass: they never write at once
	}
	ev.Run(ctx)
}

// liveInput is what the facts rules read on this host: the latest sample, the minute history, the sampler's own 5 s samples as read, the
// process scan and the start log.
func liveInput(in advisorInputs) func() (advisor.Input, bool) {
	return func() (advisor.Input, bool) {
		f, ok := in.Sampler.Latest()
		if !ok {
			return advisor.Input{}, false
		}
		inp := advisor.Input{
			Now: time.Now(), Facts: f, WorkRoot: in.WorkRoot,
			Minutes: func(since time.Time) []wire.Facts { return in.Sampler.History(since, wire.Step1m) },
			Recent:  in.Sampler.LocalRecent, // as read, not as the proto 2 gate lets records out: alerts are not behind it
			Orphans: in.Orphans,
		}
		if in.Store != nil {
			inp.UncleanStartsSince = in.Store.UncleanStartsSince
		}
		if in.Queue != nil {
			inp.Queue = func() ([]queue.QueuedJob, error) {
				ctx, cancel := context.WithTimeout(context.Background(), queueReadTimeout)
				defer cancel()
				return in.Queue.QueuedView(ctx)
			}
		}
		inp.Pool = in.Pool
		return inp, true
	}
}

// silenceInput is what the silence rules read: the pool's view of the peers and the partner's last read, with no facts.
func silenceInput(in advisorInputs) func() (advisor.Input, bool) {
	return func() (advisor.Input, bool) {
		return advisor.Input{
			Now: time.Now(), PartnerSeen: in.PartnerSeen,
			Peers: func() []advisor.PeerDown {
				var out []advisor.PeerDown
				for _, d := range in.Downs() {
					out = append(out, advisor.PeerDown{Alias: d.Alias, Since: d.Since, Liveness: d.Liveness})
				}
				return out
			},
		}, true
	}
}

// runOrphansScan is `wwd orphans-scan`: the orphans scan once, right now, on this host's own process table, printed. There is no recorded
// history of the process table to replay, so this is how the rule is checked against a real host before it is relied on. It only lists;
// the fork-loop line is one burst of table reads (the daemon needs the same loop above the limit for ten minutes before it pushes).
func runOrphansScan(measure int) error {
	src := facts.SystemSourcesWith(facts.SourceOptions{})
	now := time.Now()
	snap, ctx, err := src.Procs(now)
	if err != nil {
		return fmt.Errorf("orphans-scan: %w", err)
	}
	s := orphans.New(orphans.Config{UID: os.Getuid()})
	s.Observe(now, snap, ctx)
	r := s.Report()
	fmt.Printf("scanned %d processes as uid %d at %s\n", len(snap.Procs), os.Getuid(), now.Format(time.RFC3339))
	fmt.Printf("dev orphans (ppid 1, under a dev path, older than %s): %d\n", orphans.MinAge, len(r.Procs))
	for _, p := range r.Procs {
		fmt.Printf("  pid %-7d %-12s %-12s age %s\n", p.PID, p.Kind, p.Name, p.Age.Round(time.Minute))
	}
	fmt.Printf("fork loops (ppid-1 shell, > %.0f children/s in one burst): %d\n", orphans.LoopRate, len(r.Loops))
	for _, l := range r.Loops {
		fmt.Printf("  pid %-7d %-12s %.0f children/s\n", l.PID, l.Name, l.Rate)
	}
	if measure > 0 {
		measureOrphans(snap, ctx, measure)
	}
	return nil
}

// runAdvisorCmd handles `wwd alerts-replay --db <copy of wwd.db> [--days 7] [--work-root <dir>]`: it counts what the alert rules would have
// raised over the recorded minutes and starts of a COPY of the database (never the live file), the number each rule's PR states before it
// is switched on. It reports whether args named it.
func runAdvisorCmd(args []string) (handled bool, err error) {
	if len(args) > 0 && args[0] == "orphans-scan" {
		fs := flag.NewFlagSet("orphans-scan", flag.ContinueOnError)
		measure := fs.Int("measure", 0, "also measure the scan's wall and CPU time over this many runs")
		if err := fs.Parse(args[1:]); err != nil {
			return true, err
		}
		return true, runOrphansScan(*measure)
	}
	if len(args) == 0 || args[0] != "alerts-replay" {
		return false, nil
	}
	fs := flag.NewFlagSet("alerts-replay", flag.ContinueOnError)
	db := fs.String("db", "", "a copy of wwd.db (sqlite3 <live> \".backup <copy>\")")
	days := fs.Int("days", 7, "how many days back from the newest record")
	root := fs.String("work-root", "", "the work root of the host the database came from")
	known := fs.String("known", "", "comma-separated known residents ([advisor] known_residents of that host)")
	if err := fs.Parse(args[1:]); err != nil {
		return true, err
	}
	if *db == "" || *root == "" {
		return true, errors.New("alerts-replay: --db and --work-root are required")
	}
	st, err := store.Open(*db)
	if err != nil {
		return true, err
	}
	defer st.Close()
	// the range counts back from the newest record, so a copy analysed days later is not cut short
	newest, ok, err := st.NewestMinute()
	if err != nil {
		return true, err
	}
	if !ok {
		return true, errors.New("alerts-replay: the database holds no minutes")
	}
	recs, err := st.LoadMinutes(newest.AddDate(0, 0, -*days))
	if err != nil {
		var skipped *store.SkippedRowsError
		if !errors.As(err, &skipped) {
			return true, err
		}
		fmt.Printf("note: %v\n", err)
	}
	if len(recs) == 0 {
		return true, errors.New("alerts-replay: the database holds no minutes in that range")
	}
	// the first minutes need the starts of the hour before them (the crash-loop window)
	starts, err := st.UncleanStartTimes(recs[0].At.Add(-time.Hour))
	if err != nil {
		return true, err
	}
	var knownList []string
	if *known != "" {
		knownList = strings.Split(*known, ",")
	}
	// the replay counts the watcher's rules as if they were on (the pressure ones need the 5 s samples a replay does not have: unjudged)
	rules := advisorRules(knownList, daemon.AdvisorRule{WatchRules: true})
	res := advisor.Replay(recs, starts, *root, rules...)
	span := recs[len(recs)-1].At.Sub(recs[0].At)
	fmt.Printf("replayed %d minutes (%.1f days) from %s to %s, %d starts\n", len(recs), span.Hours()/24,
		recs[0].At.Format(time.RFC3339), recs[len(recs)-1].At.Format(time.RFC3339), len(starts))
	for _, r := range rules {
		k := r.Kind()
		o := res[k]
		fmt.Printf("%-18s episodes=%d held_minutes=%d unjudged_minutes=%d first=%s last=%s\n", k, o.Episodes, o.HeldMinutes, o.Errors,
			o.FirstAt.Format(time.RFC3339), o.LastAt.Format(time.RFC3339))
	}
	return true, nil
}
