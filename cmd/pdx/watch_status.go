package main

// `pdx watch status` (host-load-control spec §12.3): this host's view of the watcher — what it holds itself, the watcher events
// go to, the last one there was, and the settings that decide where a new one is started. Read from this host's daemon.

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/wake/purdex/internal/config"
	watchmod "github.com/wake/purdex/internal/module/watch"
)

func watchStatus(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("watch status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "config.toml (default ~/.config/pdx/config.toml)")
	asJSON := fs.Bool("json", false, "print JSON")
	rest, err := parseInterspersed(fs, args)
	if err != nil {
		return 2
	}
	if len(rest) > 0 {
		fmt.Fprint(stderr, watchUsage)
		return 2
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "pdx watch status: %v\n", err)
		return 1
	}
	res, err := doPeersRequest(http.MethodGet, watchBase(cfg)+"/api/watch/status", nil, cfg.Token, peersRequestTimeout)
	if err != nil {
		return reportPeersTransportErr(err, stderr)
	}
	switch {
	case res.status == http.StatusNotFound:
		fmt.Fprintln(stderr, "pdx watch status: this daemon has no watch support (older version?)")
		return 1
	case res.status != http.StatusOK:
		fmt.Fprintf(stderr, "pdx watch status: the daemon answered %d\n", res.status)
		return 1
	}
	if *asJSON {
		fmt.Fprintln(stdout, strings.TrimSpace(string(res.body)))
		return 0
	}
	var st watchmod.Status
	if err := json.Unmarshal(res.body, &st); err != nil {
		fmt.Fprintf(stderr, "pdx watch status: unreadable answer: %v\n", err)
		return 1
	}
	fmt.Fprint(stdout, renderWatchStatus(st))
	return 0
}

// renderWatchStatus is the status as lines of text.
func renderWatchStatus(st watchmod.Status) string {
	b := &strings.Builder{}
	onoff := map[bool]string{true: "開", false: "關"}
	fmt.Fprintf(b, "偵測 %s　自動喚醒 %s　模式 %s\n", onoff[st.Enabled], onoff[st.AutoWake], st.Mode)
	fmt.Fprintf(b, "targets  %s\n", strings.Join(st.Targets, ", "))
	fmt.Fprintf(b, "本機守著  %s", st.Self.State)
	if st.Self.SessionID != "" {
		fmt.Fprintf(b, "（session %s）", st.Self.SessionID)
	}
	b.WriteString("\n")
	if st.Self.Starting {
		b.WriteString("　啟動／喚醒進行中\n")
	}
	if until := st.Self.CooldownUntil; until.After(time.Now()) {
		fmt.Fprintf(b, "　先前的啟動沒有成功，%s 之前不再嘗試\n", until.Local().Format("01-02 15:04"))
	}
	for _, t := range []struct {
		label string
		at    time.Time
	}{{"　上次 turn", st.Self.LastTurn}, {"　上次送達", st.Self.LastDelivery}} {
		if !t.at.IsZero() {
			fmt.Fprintf(b, "%s  %s\n", t.label, t.at.Local().Format("01-02 15:04:05"))
		}
	}
	if c := st.Current; c != nil {
		fmt.Fprintf(b, "事件送往  %s（%s，session %s）\n", c.Alias, c.State, c.SessionID)
	} else {
		b.WriteString("事件送往  （還沒有記住的守門員；下次有事件時會找）\n")
	}
	if l := st.Last; l != nil {
		fmt.Fprintf(b, "上一個    %s（session %s，%s）\n", l.Alias, l.SessionID, l.At.Local().Format("01-02 15:04"))
	}
	return b.String()
}
