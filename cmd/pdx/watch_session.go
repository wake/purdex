package main

// `pdx watch start` and `pdx watch wake` (host-load-control spec §12.3–§12.4, plan W3b): make sure a watcher is awake. start looks for
// one on the targets and starts or wakes one if there is none; wake wakes the one of this host, with an optional prompt added to the
// wake prompt. Exit 0: a watcher is awake, or one is coming up (the answer says which); 1: refused or failed (a cooldown names the time).

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/wake/purdex/internal/config"
)

// sessionTimeout is how long the CLI waits: a start waits for the daemon's grace, and a peer's call is cut off sooner than that.
const sessionTimeout = 40 * time.Second

func watchSession(kind string, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("watch "+kind, flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "config.toml (default ~/.config/pdx/config.toml)")
	asJSON := fs.Bool("json", false, "print the daemon's answer")
	var prompt *string
	if kind == "wake" {
		p := fs.String("prompt", "", "text added to the wake prompt (at most 4 KB, no control characters)")
		defer func() { _ = p }()
		if err := fs.Parse(args); err != nil {
			return 2
		}
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "prompt" {
				prompt = p
			}
		})
	} else if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "多餘的參數 %q\n%s", fs.Args(), watchUsage)
		return 2
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "pdx watch %s: %v\n", kind, err)
		return 1
	}
	body := []byte("{}")
	if prompt != nil {
		body, _ = json.Marshal(map[string]string{"prompt": *prompt})
	}
	res, err := doPeersRequest(http.MethodPost, watchBase(cfg)+"/api/watch/"+kind, body, cfg.Token, sessionTimeout)
	if err != nil {
		return reportPeersTransportErr(err, stderr)
	}
	switch {
	case res.status == http.StatusNotFound:
		fmt.Fprintf(stderr, "pdx watch %s: this daemon has no watch %s support (older version?)\n", kind, kind)
		return 1
	case res.status == http.StatusConflict && strings.Contains(string(res.body), `"cooldown"`):
		var e struct{ Detail string }
		_ = json.Unmarshal(res.body, &e)
		fmt.Fprintf(stderr, "pdx watch %s: 先前的啟動沒有成功，%s 之前不再嘗試\n", kind, sanitizeCell(e.Detail))
		return 1
	case res.status != http.StatusOK && res.status != http.StatusAccepted:
		fmt.Fprintf(stderr, "pdx watch %s: %s\n", kind, sanitizeCell(extractPeersErrorMessage(res.body)))
		return 1
	}
	if *asJSON {
		fmt.Fprintln(stdout, strings.TrimSpace(string(res.body)))
		return 0
	}
	var out struct {
		Host      string `json:"host"`
		HostID    string `json:"host_id"`
		State     string `json:"state"`
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(res.body, &out)
	where := out.Host
	if where == "" {
		where = out.HostID
	}
	if res.status == http.StatusAccepted {
		fmt.Fprintf(stdout, "守門員正在 %s 啟動中（背景繼續，之後用 pdx watch status 查看）\n", sanitizeCell(where))
		return 0
	}
	fmt.Fprintf(stdout, "守門員醒著：%s  %s\n", sanitizeCell(where), sanitizeCell(out.SessionID))
	return 0
}
