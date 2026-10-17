package main

// `pdx watch ack` (host-load-control spec §12.3): the watcher marks events handled, with a note. The daemon of this host does it
// (POST /api/watch/ack); the exit code is not 0 when any id was not handled, and the ids that were not are listed.

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/watch"
)

// ackWire is the daemon's answer to POST /api/watch/ack.
type ackWire struct {
	Results   []watch.AckResult `json:"results"`
	Handled   int               `json:"handled"`
	Unhandled int               `json:"unhandled"`
	// Hosts is what is left on each target host (this one first); Unhandled is their sum, -1 when one did not say.
	Hosts []ackHostLeft `json:"hosts,omitempty"`
}

// ackHostLeft is the pending count of one host (-1: it did not say).
type ackHostLeft struct {
	Host      string `json:"host"`
	Unhandled int    `json:"unhandled"`
}

func watchAck(args []string, stdout, stderr io.Writer) int {
	fs, af := newAckFlagSet(stderr)
	note, all, host, asJSON, cfgPath := af.note, af.all, af.host, af.asJSON, af.cfgPath
	ids, err := parseInterspersed(fs, args)
	if err != nil {
		return 2
	}
	if *host != "" && !*all {
		fmt.Fprint(stderr, "pdx watch ack: --host is for --all (an event id already says which host it is on)\n"+watchUsage)
		return 2
	}
	if (len(ids) == 0) == !*all {
		fmt.Fprint(stderr, "pdx watch ack: name the event ids, or --all\n"+watchUsage)
		return 2
	}
	if err := watch.ValidateNote(*note); err != nil {
		fmt.Fprintf(stderr, "pdx watch ack: --note: %v\n", err)
		return 2
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "pdx watch ack: %v\n", err)
		return 1
	}
	body, _ := json.Marshal(map[string]any{"ids": ids, "all": *all, "host": *host, "note": *note, "origin_inbox": os.Getenv("CLAUDE_CODE_MESSAGING_SOCKET")})
	res, err := doPeersRequest(http.MethodPost, watchBase(cfg)+"/api/watch/ack", body, cfg.Token, peersRequestTimeout)
	if err != nil {
		return reportPeersTransportErr(err, stderr)
	}
	switch {
	case res.status == http.StatusNotFound:
		fmt.Fprintln(stderr, "pdx watch ack: this daemon has no watch support (older version?)")
		return 1
	case res.status != http.StatusOK:
		fmt.Fprintf(stderr, "pdx watch ack: %s\n", sanitizeCell(extractPeersErrorMessage(res.body)))
		return 1
	}
	var out ackWire
	if err := json.Unmarshal(res.body, &out); err != nil {
		fmt.Fprintf(stderr, "pdx watch ack: unreadable answer: %v\n", err)
		return 1
	}
	if *asJSON {
		fmt.Fprintln(stdout, strings.TrimSpace(string(res.body)))
	} else {
		fmt.Fprint(stdout, renderAck(out))
	}
	if failed := ackFailures(out); len(failed) > 0 {
		fmt.Fprintf(stderr, "pdx watch ack: %d 筆沒有處理到：%s\n", len(failed), strings.Join(failed, " "))
		return 1
	}
	return 0
}

// ackFlags are the flags of `pdx watch ack`.
type ackFlags struct {
	note, host, cfgPath *string
	all, asJSON         *bool
}

// newAckFlagSet is the flag set of `pdx watch ack`; the test that checks every command a message mentions exists reads the same one.
func newAckFlagSet(w io.Writer) (*flag.FlagSet, *ackFlags) {
	fs := flag.NewFlagSet("watch ack", flag.ContinueOnError)
	fs.SetOutput(w)
	return fs, &ackFlags{
		note:    fs.String("note", "", "what was done about it, one line of at most 500 bytes (required)"),
		all:     fs.Bool("all", false, "every pending event of this host (with --host: of that host)"),
		host:    fs.String("host", "", "with --all: the paired host whose pending events are meant"),
		asJSON:  fs.Bool("json", false, "print the daemon's answer"),
		cfgPath: fs.String("config", "", "config.toml (default ~/.config/pdx/config.toml)"),
	}
}

// ackFailures are the ids that were not handled and are not handled already.
func ackFailures(a ackWire) []string {
	var out []string
	for _, r := range a.Results {
		if r.Outcome != watch.AckHandled && r.Outcome != watch.AckAlready {
			out = append(out, r.ID)
		}
	}
	return out
}

// renderAck is the answer as lines of text.
func renderAck(a ackWire) string {
	b := &strings.Builder{}
	for _, r := range a.Results {
		switch r.Outcome {
		case watch.AckHandled:
			fmt.Fprintf(b, "已處理  %s\n", r.ID)
		case watch.AckAlready:
			fmt.Fprintf(b, "先前已處理（備註不變）  %s\n", r.ID)
		case watch.AckUnreachable:
			fmt.Fprintf(b, "沒有處理到（那台主機連不上）  %s\n", r.ID)
		case watch.AckUnsupported:
			fmt.Fprintf(b, "沒有處理到（那台主機的 daemon 太舊，沒有這個路由）  %s\n", r.ID)
		case watch.AckRefused:
			fmt.Fprintf(b, "沒有處理到（那台主機拒絕了）  %s\n", r.ID)
		case watch.AckAmbiguous:
			fmt.Fprintf(b, "沒有處理到（有兩台主機同名，無法判斷這筆在哪一台）  %s\n", r.ID)
		default:
			fmt.Fprintf(b, "沒有處理到（%s）  %s\n", r.Outcome, r.ID)
		}
	}
	if len(a.Hosts) > 0 { // per host, so a count of one host is never read as "everything is done"
		parts := make([]string, len(a.Hosts))
		known := 0
		for i, h := range a.Hosts {
			if h.Unhandled < 0 {
				parts[i] = sanitizeCell(h.Host) + " ?"
				continue
			}
			known += h.Unhandled
			parts[i] = fmt.Sprintf("%s %d", sanitizeCell(h.Host), h.Unhandled)
		}
		if a.Unhandled < 0 {
			fmt.Fprintf(b, "這次處理 %d 筆；待處理數不完整（%s），問不到的主機用 pdx watch log --all --unhandled 看\n", a.Handled, strings.Join(parts, "、"))
			return b.String()
		}
		fmt.Fprintf(b, "這次處理 %d 筆，各主機還有 %d 筆待處理（%s）\n", a.Handled, known, strings.Join(parts, "、"))
		return b.String()
	}
	if a.Unhandled < 0 { // the host that was asked did not say how many are left
		fmt.Fprintf(b, "這次處理 %d 筆\n", a.Handled)
		return b.String()
	}
	fmt.Fprintf(b, "這次處理 %d 筆，還有 %d 筆待處理\n", a.Handled, a.Unhandled)
	return b.String()
}
