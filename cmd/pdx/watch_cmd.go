package main

// `pdx watch log` (host-load-control spec §12): read the load watcher's event log straight from its directory, so it works with
// the daemon down.

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/watch"
)

const watchUsage = `usage: pdx watch status [--json] [--config <path>]
       pdx watch config [get [key] | set key=value…] [--json] [--config <path>]
       pdx watch log [--host <alias> | --all] [--since <24h|7d|2026-10-14>] [--kind unmanaged_heavy|pressure_high|slot_orphaned|slot_long] [--unhandled] [--json] [--config <path>]
       pdx watch start [--json] [--config <path>]
       pdx watch wake [--prompt "<text>"] [--json] [--config <path>]
       pdx watch wake-settings [show] [--json] [--config <path>]
       pdx watch wake-settings set <path>=<value>… [--json] [--config <path>]    (path: rules.<rule>.<field>, [i] for an element; a list is comma separated, @file reads a file, @@ is a literal @)
       pdx watch ack <id>… | --all [--host <alias>]  --note "<one line>" [--json] [--config <path>]
`

func runWatch(args []string) {
	os.Exit(watchMain(args, os.Stdout, os.Stderr))
}

// watchMain is runWatch with its streams, for tests; it returns the exit code.
func watchMain(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "status" {
		return watchStatus(args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "config" {
		return watchConfig(args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "wake-settings" {
		return watchWakeSettings(args[1:], stdout, stderr)
	}
	if len(args) > 0 && (args[0] == "start" || args[0] == "wake") {
		return watchSession(args[0], args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "ack" {
		return watchAck(args[1:], stdout, stderr)
	}
	if len(args) == 0 || args[0] != "log" {
		fmt.Fprint(stderr, watchUsage)
		return 2
	}
	fs, lf := newLogFlagSet(stderr)
	since, kind, unhandled, asJSON, dir, cfgPath := lf.since, lf.kind, lf.unhandled, lf.asJSON, lf.dir, lf.cfgPath
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() > 0 { // flag stops at the first positional word: what follows would be ignored silently
		fmt.Fprintf(stderr, "多餘的參數 %q\n%s", fs.Args(), watchUsage)
		return 2
	}
	if *lf.all && *lf.host != "" {
		fmt.Fprintf(stderr, "--all 與 --host 只能擇一\n%s", watchUsage)
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
	if *lf.all || *lf.host != "" {
		return watchLogViaDaemon(lf, from, stdout, stderr)
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
	acks, aerr := watch.ReadAcks(d)
	if aerr != nil {
		fmt.Fprintf(stderr, "注意：讀不到處理紀錄（%v），事件都當作待處理\n", aerr)
	}
	if *unhandled {
		kept := recs[:0]
		for _, r := range recs {
			if _, done := acks[r.ID]; !done {
				kept = append(kept, r)
			}
		}
		recs = kept
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
			var ack *watch.Ack
			if a, done := acks[r.ID]; done {
				ack = &a
			}
			b, _ := json.Marshal(watch.HandledRecord(r, ack))
			fmt.Fprintln(stdout, string(b))
			continue
		}
		line := plainText(watchLine(r))
		if a, done := acks[r.ID]; done {
			line += "  ✓ " + plainText(a.Note)
		}
		fmt.Fprintln(stdout, line)
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

// logFlags are the flags of `pdx watch log`.
type logFlags struct {
	since, kind, host, dir, cfgPath *string
	unhandled, asJSON, all          *bool
}

// newLogFlagSet is the flag set of `pdx watch log`; the test that checks every command a message mentions exists reads the same one.
func newLogFlagSet(w io.Writer) (*flag.FlagSet, *logFlags) {
	fs := flag.NewFlagSet("watch log", flag.ContinueOnError)
	fs.SetOutput(w)
	return fs, &logFlags{
		since:     fs.String("since", "", "only events from this long ago (24h, 7d) or this date (2026-10-14)"),
		kind:      fs.String("kind", "", "only this kind of event"),
		host:      fs.String("host", "", "read the events of this paired host (through the daemon)"),
		all:       fs.Bool("all", false, "read the events of every host, this one included (through the daemon)"),
		unhandled: fs.Bool("unhandled", false, "only the events no one has marked handled"),
		asJSON:    fs.Bool("json", false, "one JSON record per line"),
		dir:       fs.String("dir", "", "the watcher's directory (default: <data_dir>/watch of the config)"),
		cfgPath:   fs.String("config", "", "config.toml (default ~/.config/pdx/config.toml): its data_dir says where the daemon keeps the log"),
	}
}

// hostLogWire is the daemon's answer to GET /api/watch/log.
type hostLogWire struct {
	Events []watch.EventView `json:"events"`
	Hosts  []struct {
		Host  string `json:"host"`
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	} `json:"hosts"`
}

// watchLogViaDaemon reads the events of another host, or of all, through the daemon of this host (which asks the paired hosts). A host
// that could not be read is named and makes the exit code 1; the events of the others are printed all the same.
func watchLogViaDaemon(lf *logFlags, from time.Time, stdout, stderr io.Writer) int {
	cfg, err := config.Load(*lf.cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "pdx watch log: %v\n", err)
		return 1
	}
	q := url.Values{}
	if *lf.all {
		q.Set("host", "all")
	} else {
		q.Set("host", *lf.host)
	}
	if !from.IsZero() {
		q.Set("since", from.UTC().Format(time.RFC3339))
	}
	if *lf.kind != "" {
		q.Set("kind", *lf.kind)
	}
	if *lf.unhandled {
		q.Set("unhandled", "1")
	}
	res, err := doPeersRequest(http.MethodGet, watchBase(cfg)+"/api/watch/log?"+q.Encode(), nil, cfg.Token, peersRequestTimeout)
	if err != nil {
		return reportPeersTransportErr(err, stderr)
	}
	switch {
	case res.status == http.StatusNotFound:
		fmt.Fprintln(stderr, "pdx watch log: this daemon has no host log route (older version?)")
		return 1
	case res.status != http.StatusOK:
		fmt.Fprintf(stderr, "pdx watch log: %s\n", sanitizeCell(extractPeersErrorMessage(res.body)))
		return 1
	}
	var out hostLogWire
	if err := json.Unmarshal(res.body, &out); err != nil {
		fmt.Fprintf(stderr, "pdx watch log: unreadable answer: %v\n", err)
		return 1
	}
	code := 0
	for _, h := range out.Hosts {
		if !h.OK {
			fmt.Fprintf(stderr, "注意：%s 的事件讀不到：%s\n", plainText(h.Host), plainText(h.Error))
			code = 1
		}
	}
	if len(out.Events) == 0 && !*lf.asJSON {
		fmt.Fprintln(stdout, "沒有符合的事件")
		return code
	}
	for _, e := range out.Events {
		if *lf.asJSON {
			b, _ := json.Marshal(e)
			fmt.Fprintln(stdout, string(b))
			continue
		}
		fmt.Fprintln(stdout, plainText(eventViewLine(e)))
	}
	return code
}

// eventViewLine is one event of the daemon's view as a line of text: time, host, kind, what it was about, and the note when handled.
func eventViewLine(e watch.EventView) string {
	b := &strings.Builder{}
	fmt.Fprintf(b, "%s  %-6s %-15s", e.At.Local().Format("01-02 15:04:05"), e.Host, e.Kind)
	switch e.Kind {
	case watch.KindUnmanagedHeavy:
		fmt.Fprintf(b, "  %s  %s  %.1f 核 %d MB %s", e.Label, e.Command, e.PeakCores, e.PeakMB, time.Duration(e.DurationS*float64(time.Second)).Round(time.Second))
	case watch.KindPressureHigh:
		b.WriteString("  " + e.Detail)
	default:
		fmt.Fprintf(b, "  %s  gate %d", e.Slot, e.HolderPID)
	}
	if e.Status == watch.StatusHandled {
		b.WriteString("  ✓ " + e.Note)
		if e.HandledFromHost != "" {
			b.WriteString("（" + e.HandledFromHost + "）")
		}
	}
	return b.String()
}
