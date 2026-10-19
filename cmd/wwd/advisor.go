package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/wake/purdex/internal/wwd/advisor"
	"github.com/wake/purdex/internal/wwd/daemon"
	"github.com/wake/purdex/internal/wwd/store"
	"github.com/wake/purdex/internal/wwd/wire"
)

// advisorInputs is what the alert rules (spec §13.3) read on this host.
type advisorInputs struct {
	Alias    string
	WorkRoot string
	Store    *store.Store
	Broker   *daemon.Broker
	Sampler  *daemon.Sampler
	// Known is [advisor] known_residents: the residents whose large footprint is expected here.
	Known []string
}

// advisorRules are the rules this build judges (spec §13.3 except silence, thrash's broker hand-off and the incidents of 8b); the live
// evaluator and the replay run the same list.
func advisorRules(known []string) []advisor.Rule {
	return []advisor.Rule{
		advisor.DiskLow{}, advisor.DiskFalling{}, advisor.WorkerCrashLoop{},
		advisor.PressureHeld{}, advisor.Thrash{}, advisor.ResidentGrowth{Known: known},
	}
}

// runAdvisor judges the alert rules over this host's own facts until ctx ends. The first judgement waits one interval, so the first
// facts sample and the start record are in before a rule looks.
func runAdvisor(ctx context.Context, in advisorInputs) {
	book := daemon.NewAlertBook(in.Store, in.Alias, in.Broker, nil)
	input := func() (advisor.Input, bool) {
		f, ok := in.Sampler.Latest()
		if !ok {
			return advisor.Input{}, false
		}
		return advisor.Input{
			Now: time.Now(), Facts: f, WorkRoot: in.WorkRoot,
			Minutes:     func(since time.Time) []wire.Facts { return in.Sampler.History(since, wire.Step1m) },
			Recent:      func(since time.Time) []wire.Facts { return in.Sampler.History(since, wire.Step5s) },
			StartsSince: in.Store.StartsSince,
		}, true
	}
	// not behind the proto 2 writer gate (spec §13.1): an unreachable host must not silence the others' alerts
	advisor.NewEvaluator(book, input, log.Printf, advisorRules(in.Known)...).Run(ctx)
}

// runAdvisorCmd handles `wwd alerts-replay --db <copy of wwd.db> [--days 7] [--work-root <dir>]`: it counts what the alert rules would have
// raised over the recorded minutes and starts of a COPY of the database (never the live file), the number each rule's PR states before it
// is switched on. It reports whether args named it.
func runAdvisorCmd(args []string) (handled bool, err error) {
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
	starts, err := st.StartTimes(recs[0].At.Add(-time.Hour))
	if err != nil {
		return true, err
	}
	var knownList []string
	if *known != "" {
		knownList = strings.Split(*known, ",")
	}
	rules := advisorRules(knownList)
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
