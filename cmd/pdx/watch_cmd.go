package main

// `pdx watch log` (host-load-control spec §12): read the load watcher's event log straight from its directory, so it works with
// the daemon down.

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/watch"
)

const watchUsage = `usage: pdx watch log [--since <24h|7d|2026-10-14>] [--kind unmanaged_heavy|pressure_high|slot_orphaned|slot_long] [--json] [--config <path>]
`

func runWatch(args []string) {
	os.Exit(watchMain(args, os.Stdout, os.Stderr))
}

// watchMain is runWatch with its streams, for tests; it returns the exit code.
func watchMain(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "log" {
		fmt.Fprint(stderr, watchUsage)
		return 2
	}
	fs := flag.NewFlagSet("watch log", flag.ContinueOnError)
	fs.SetOutput(stderr)
	since := fs.String("since", "", "only events from this long ago (24h, 7d) or this date (2026-10-14)")
	kind := fs.String("kind", "", "only this kind of event")
	asJSON := fs.Bool("json", false, "one JSON record per line")
	dir := fs.String("dir", "", "the watcher's directory (default: <data_dir>/watch of the config)")
	cfgPath := fs.String("config", "", "config.toml (default ~/.config/pdx/config.toml): its data_dir says where the daemon keeps the log")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() > 0 { // flag stops at the first positional word: what follows would be ignored silently
		fmt.Fprintf(stderr, "多餘的參數 %q\n%s", fs.Args(), watchUsage)
		return 2
	}
	var from time.Time
	if *since != "" {
		t, err := parseSince(*since, time.Now())
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		from = t
	}
	switch watch.Kind(*kind) {
	case "", watch.KindUnmanagedHeavy, watch.KindPressureHigh, watch.KindSlotOrphaned, watch.KindSlotLong:
	default:
		fmt.Fprintf(stderr, "未知的 --kind %q\n", *kind)
		return 2
	}
	d := *dir
	if d == "" {
		d = watchDirFor(*cfgPath)
	}
	recs, skipped, err := watch.ReadLogDiag(d, from, watch.Kind(*kind))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if skipped > 0 {
		fmt.Fprintf(stderr, "注意：略過 %d 行無法解析的紀錄（當機留下的半行，或檔案損毀）\n", skipped)
	}
	if len(recs) == 0 && !*asJSON {
		fmt.Fprintln(stdout, "沒有符合的事件")
		return 0
	}
	for _, r := range recs {
		if *asJSON {
			b, _ := json.Marshal(r)
			fmt.Fprintln(stdout, string(b))
			continue
		}
		fmt.Fprintln(stdout, plainText(watchLine(r)))
	}
	return 0
}

// watchDirFor is where the daemon of that config keeps the watcher's files: <data_dir>/watch. A config that cannot be read
// falls back to the default directory.
func watchDirFor(cfgPath string) string {
	if cfgPath == "" {
		cfgPath = config.DefaultPath()
	}
	if cfg, err := config.Load(cfgPath); err == nil && cfg.DataDir != "" {
		return filepath.Join(cfg.DataDir, "watch")
	}
	return watch.DefaultDir()
}

// parseSince reads "36h", "7d" (days of 24 hours) or a date "2026-10-14" (local midnight).
func parseSince(s string, now time.Time) (time.Time, error) {
	if n, ok := strings.CutSuffix(s, "d"); ok {
		if days, err := strconv.ParseFloat(n, 64); err == nil && days >= 0 {
			return now.Add(-time.Duration(days * float64(24*time.Hour))), nil
		}
	}
	if d, err := time.ParseDuration(s); err == nil && d >= 0 {
		return now.Add(-d), nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("--since 看不懂 %q（用 24h、7d 或 2026-10-14）", s)
}

// watchLine is one record as a line of text.
func watchLine(r watch.Record) string {
	b := &strings.Builder{}
	fmt.Fprintf(b, "%s  %-15s", r.At.Local().Format("01-02 15:04:05"), r.Kind)
	switch r.Kind {
	case watch.KindUnmanagedHeavy:
		fmt.Fprintf(b, "  %s  %s  %.1f 核 %d MB %s", r.Label, r.Signature, r.PeakCores, r.PeakMB, time.Duration(r.DurationS*float64(time.Second)).Round(time.Second))
		if r.New {
			b.WriteString("  新")
		}
		if r.Cwd != "" {
			b.WriteString("  " + r.Cwd)
		}
	case watch.KindPressureHigh:
		b.WriteString("  " + r.Detail)
	default:
		fmt.Fprintf(b, "  %s  gate pid %d", r.Slot, r.HolderPID)
		if len(r.Holders) > 0 {
			fmt.Fprintf(b, "  鎖由 %v 持有", r.Holders)
		}
	}
	return b.String()
}

// plainText replaces the control characters of s (a cwd or a command word can carry a newline or an escape sequence) so a
// line of the text view stays one line and cannot drive the terminal.
func plainText(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return '?'
		}
		return r
	}, s)
}
