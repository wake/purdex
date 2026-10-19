package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
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
	// HintsOpen is the proto 2 gate for the `alert` event; the alerts table is written whatever it says (daemon.AlertBook).
	HintsOpen func() bool
}

// runAdvisor judges the alert rules over this host's own facts until ctx ends. The first judgement waits one interval, so the first
// facts sample and the start record are in before a rule looks.
func runAdvisor(ctx context.Context, in advisorInputs) {
	book := daemon.NewAlertBook(in.Store, in.Alias, in.Broker, in.HintsOpen, nil)
	input := func() (advisor.Input, bool) {
		f, ok := in.Sampler.Latest()
		if !ok {
			return advisor.Input{}, false
		}
		return advisor.Input{
			Now: time.Now(), Facts: f, WorkRoot: in.WorkRoot,
			Minutes:     func(since time.Time) []wire.Facts { return in.Sampler.History(since, wire.Step1m) },
			StartsSince: in.Store.StartsSince,
		}, true
	}
	advisor.NewEvaluator(book, input, log.Printf, advisor.DiskLow{}, advisor.DiskFalling{}, advisor.WorkerCrashLoop{}).Run(ctx)
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
	recs, err := st.LoadMinutes(time.Now().AddDate(0, 0, -*days))
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
	starts, err := st.StartTimes(recs[0].At)
	if err != nil {
		return true, err
	}
	res := advisor.Replay(recs, starts, *root, advisor.DiskLow{}, advisor.DiskFalling{}, advisor.WorkerCrashLoop{})
	span := recs[len(recs)-1].At.Sub(recs[0].At)
	fmt.Printf("replayed %d minutes (%.1f days) from %s to %s, %d starts\n", len(recs), span.Hours()/24,
		recs[0].At.Format(time.RFC3339), recs[len(recs)-1].At.Format(time.RFC3339), len(starts))
	for _, k := range []wire.AlertKind{wire.AlertDiskLow, wire.AlertDiskFalling, wire.AlertWorkerCrash} {
		o := res[k]
		fmt.Printf("%-18s episodes=%d held_minutes=%d unjudged_minutes=%d first=%s last=%s\n", k, o.Episodes, o.HeldMinutes, o.Errors,
			o.FirstAt.Format(time.RFC3339), o.LastAt.Format(time.RFC3339))
	}
	return true, nil
}
