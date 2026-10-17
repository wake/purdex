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
}

func watchAck(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("watch ack", flag.ContinueOnError)
	fs.SetOutput(stderr)
	note := fs.String("note", "", "what was done about it, one line of at most 500 bytes (required)")
	all := fs.Bool("all", false, "every pending event of this host")
	asJSON := fs.Bool("json", false, "print the daemon's answer")
	cfgPath := fs.String("config", "", "config.toml (default ~/.config/pdx/config.toml)")
	ids, err := parseInterspersed(fs, args)
	if err != nil {
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
	body, _ := json.Marshal(map[string]any{"ids": ids, "all": *all, "note": *note, "origin_inbox": os.Getenv("CLAUDE_CODE_MESSAGING_SOCKET")})
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
		default:
			fmt.Fprintf(b, "沒有處理到（%s）  %s\n", r.Outcome, r.ID)
		}
	}
	fmt.Fprintf(b, "這次處理 %d 筆，還有 %d 筆待處理\n", a.Handled, a.Unhandled)
	return b.String()
}
