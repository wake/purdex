package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/wake/purdex/internal/gate"
)

const gateHistoryUsage = `usage: pdx gate history [--host <name>] [--since <duration or RFC 3339 time>] [--limit <n>] [--json] [--config <path>]
  The commands that were offered to an assist host, newest first: where they ran (- = here after all), the seconds to send and
  to run, the exit code, and why a send did not run the command there. --since 2h = the last two hours.
`

func gateHistory(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gate history", flag.ContinueOnError)
	fs.SetOutput(stderr)
	host := fs.String("host", "", "only this assist host")
	since := fs.String("since", "", "only records newer than this (a duration like 2h, or an RFC 3339 time)")
	limit := fs.Int("limit", 50, "at most this many records")
	asJSON := fs.Bool("json", false, "one JSON array")
	cfgPath := fs.String("config", "", "config file")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		fmt.Fprint(stderr, gateHistoryUsage)
		return 2
	}
	f := gate.HistoryFilter{Host: *host, Limit: *limit}
	if *since != "" {
		if d, err := time.ParseDuration(*since); err == nil && d > 0 {
			f.Since = time.Now().Add(-d)
		} else if t, err := time.Parse(time.RFC3339, *since); err == nil {
			f.Since = t
		} else {
			fmt.Fprintf(stderr, "pdx gate history: --since %q is neither a duration nor an RFC 3339 time\n", *since)
			return 2
		}
	}
	cfg := loadGateConfig(*cfgPath, stderr)
	recs, err := gate.History(cfg.Dir, f)
	if err != nil {
		fmt.Fprintf(stderr, "pdx gate history: %v\n", err)
		return 1
	}
	if *asJSON {
		if recs == nil {
			recs = []gate.Decision{}
		}
		b, _ := json.Marshal(recs)
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	if len(recs) == 0 {
		fmt.Fprintln(stdout, "no command was offered to an assist host in that time")
		return 0
	}
	for _, d := range recs {
		ran := d.Host
		if ran == "" {
			ran = "-"
		}
		line := fmt.Sprintf("%s  %-8s send %5.1fs  run %6.1fs  exit %-3d %s", d.Time.Local().Format("01-02 15:04:05"), ran, d.TransferS, d.RunS, d.Exit, strings.Join(d.Argv, " "))
		if d.Reclaimed {
			line += "  [taken back]"
		}
		if d.AssistWhy != "" {
			line += "  [" + d.Tried + ": " + d.AssistWhy + "]"
		}
		fmt.Fprintln(stdout, line)
	}
	return 0
}
