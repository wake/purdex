package main

// `pdx watch wake-settings` (watcher wake settings spec §5 and §7, plan WK-4): show the document that says when the watcher is woken, and
// change it. The document is the daemon's (GET and PUT /api/watch/wake-settings); the CLI reads it, changes the paths it is told to, and
// writes the whole of it back with the etag it read (a PUT replaces the document and a stale etag is a 409). `pdx watch wake` is the manual
// wake-up and is not this.
//
// Exit codes: 0 done; 1 refused or failed (a source that cannot be reached, a device token, the daemon); 2 usage, and anything wrong with
// the paths and values, which is found before anything is sent; 3 someone changed it first (409), run it again; 4 the daemon refused the
// document (400): each field is named.

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/wake/purdex/internal/config"
)

const (
	wakeExitConflict = 3
	wakeExitInvalid  = 4
	wakeTimeout      = 15 * time.Second
	wakeSuppressedN  = 10 // how many of the daemon's last refused wake-ups `show` lists
)

// wakeView is the daemon's answer to GET and the 200 of PUT, as far as the CLI reads it. The settings stay raw: the CLI changes them as a
// tree and sends them back whole, whatever fields a newer daemon adds.
type wakeView struct {
	Settings   json.RawMessage `json:"settings"`
	Etag       string          `json:"etag"`
	Revision   int64           `json:"revision"`
	Source     string          `json:"source"`
	SourceID   string          `json:"source_id"`
	IsSource   bool            `json:"is_source"`
	Stale      bool            `json:"stale"`
	Readonly   bool            `json:"readonly"`
	FetchedAt  *string         `json:"fetched_at"`
	Suppressed []struct {
		At     string `json:"at"`
		Host   string `json:"host"`
		Reason string `json:"reason"`
		Why    string `json:"why"`
		Detail string `json:"detail"`
	} `json:"suppressed"`
	PatrolStatus struct {
		LastOKAt       *string `json:"last_ok_at"`
		LastOKHost     *string `json:"last_ok_host"`
		LastFailAt     *string `json:"last_fail_at"`
		LastFailCode   *string `json:"last_fail_code"`
		LastFailDetail *string `json:"last_fail_detail"`
	} `json:"patrol_status"`
}

// parseWakeView reads the daemon's document answer (the GET, and the 200 of a PUT, which is the same): settings must be a JSON object, not
// null, an array or a scalar, and there must be an etag. ok is false for anything else (a proxy's page, a wrong version).
func parseWakeView(body []byte) (v wakeView, ok bool) {
	if err := json.Unmarshal(body, &v); err != nil || v.Etag == "" {
		return wakeView{}, false
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(v.Settings, &obj); err != nil || obj == nil {
		return wakeView{}, false
	}
	var typed wakeDocView // the fields `show` reads have to be of the types it reads them as
	if err := json.Unmarshal(v.Settings, &typed); err != nil {
		return wakeView{}, false
	}
	return v, true
}

func watchWakeSettings(args []string, stdout, stderr io.Writer) int {
	// flags and the words are mixed (`set a=1 --json`): a word is never a flag (a path=value does not start with "-"), so the flags are taken out first
	var flagArgs, words []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--config" || a == "-config":
			flagArgs = append(flagArgs, a)
			if i+1 < len(args) {
				i++
				flagArgs = append(flagArgs, args[i])
			}
		case strings.HasPrefix(a, "-"):
			flagArgs = append(flagArgs, a)
		default:
			words = append(words, a)
		}
	}
	fs := flag.NewFlagSet("watch wake-settings", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "config.toml (default ~/.config/pdx/config.toml)")
	asJSON := fs.Bool("json", false, "print the daemon's answer")
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	sub := "show"
	if len(words) > 0 {
		sub, words = words[0], words[1:]
	}
	switch sub {
	case "show":
		if len(words) > 0 {
			fmt.Fprintf(stderr, "多餘的參數 %q\n%s", words, watchUsage)
			return 2
		}
	case "set":
		if len(words) == 0 {
			fmt.Fprintf(stderr, "pdx watch wake-settings set: 要給至少一個 <path>=<value>\n%s", watchUsage)
			return 2
		}
	default:
		fmt.Fprintf(stderr, "pdx watch wake-settings: 不認得的子命令 %q（show 或 set）\n%s", sub, watchUsage)
		return 2
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "pdx watch wake-settings: %v\n", err)
		return 1
	}
	res, err := doPeersRequest(http.MethodGet, watchBase(cfg)+wakeSettingsPath, nil, cfg.Token, wakeTimeout)
	if err != nil {
		return reportPeersTransportErr(err, stderr)
	}
	if res.status != http.StatusOK {
		return wakeFailure("pdx watch wake-settings", res.status, res.body, stderr)
	}
	view, ok := parseWakeView(res.body)
	if !ok {
		fmt.Fprintln(stderr, "pdx watch wake-settings: the daemon's answer is not a settings document (older version?)")
		return 1
	}
	if sub == "show" {
		if *asJSON {
			fmt.Fprintln(stdout, strings.TrimSpace(string(res.body)))
			return 0
		}
		printWakeView(stdout, view)
		return 0
	}
	return setWakeSettings(cfg, view, words, *asJSON, stdout, stderr)
}

const wakeSettingsPath = "/api/watch/wake-settings"

// wakeFailure prints a daemon answer that is not the one wanted, by its `error` (and `detail`), and returns 1.
func wakeFailure(who string, status int, body []byte, stderr io.Writer) int {
	var e struct {
		Error, Detail, Source string
	}
	_ = json.Unmarshal(body, &e)
	switch {
	case status == http.StatusNotFound:
		fmt.Fprintf(stderr, "%s: this daemon has no wake settings (older version?)\n", who)
	case status == http.StatusForbidden && e.Error == "device_forbidden":
		fmt.Fprintf(stderr, "%s: device_forbidden — 這是配對手機的裝置 token，只能讀、不能改喚醒設定（唯讀）；用這台主機的管理 token\n", who)
	case status == http.StatusServiceUnavailable && e.Error == "source_unreachable":
		fmt.Fprintf(stderr, "%s: 正本連不上（正本是 %s）；設定只能寫在正本，等它連得上再改\n", who, sanitizeCell(e.Source))
	default:
		msg := e.Error
		if msg == "" {
			msg = strings.TrimSpace(string(body))
		}
		if e.Detail != "" {
			msg += "：" + e.Detail
		}
		fmt.Fprintf(stderr, "%s: %d %s\n", who, status, sanitizeCell(msg))
	}
	return 1
}

// setWakeSettings applies the `path=value` words to the document of view and writes it back with view's etag.
func setWakeSettings(cfg config.Config, view wakeView, words []string, asJSON bool, stdout, stderr io.Writer) int {
	dec := json.NewDecoder(bytes.NewReader(view.Settings))
	dec.UseNumber() // numbers stay as they were read
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		fmt.Fprintf(stderr, "pdx watch wake-settings: the settings the daemon sent cannot be read: %v\n", err)
		return 1
	}
	for _, w := range words {
		path, raw, ok := strings.Cut(w, "=")
		if !ok || path == "" {
			fmt.Fprintf(stderr, "pdx watch wake-settings set: %q 不是 <path>=<value>\n", w)
			return 2
		}
		if err := applyWakePath(doc, path, raw); err != nil {
			fmt.Fprintf(stderr, "pdx watch wake-settings set: %s: %v\n", path, err)
			return 2
		}
	}
	body, err := json.Marshal(map[string]any{"settings": doc, "expect_etag": view.Etag})
	if err != nil {
		fmt.Fprintf(stderr, "pdx watch wake-settings set: %v\n", err)
		return 1
	}
	res, err := doPeersRequest(http.MethodPut, watchBase(cfg)+wakeSettingsPath, body, cfg.Token, wakeTimeout)
	if err != nil {
		return reportPeersTransportErr(err, stderr)
	}
	switch res.status {
	case http.StatusOK:
		// a 200 that is not a settings document (a proxy's page, an empty body) does not say the write happened: it may have, so say that it
		// cannot be told, and print nothing as the daemon's answer
		done, ok := parseWakeView(res.body)
		if !ok {
			fmt.Fprintln(stderr, "pdx watch wake-settings set: 伺服器回了 200 但內容不是設定文件，寫入結果無法確認；用 pdx watch wake-settings 看現在的內容")
			return 1
		}
		if asJSON {
			fmt.Fprintln(stdout, strings.TrimSpace(string(res.body)))
			return 0
		}
		fmt.Fprintf(stdout, "已更新  revision %d  etag %s\n", done.Revision, sanitizeCell(done.Etag))
		return 0
	case http.StatusConflict:
		var c struct {
			Current wakeView `json:"current"`
		}
		_ = json.Unmarshal(res.body, &c)
		fmt.Fprintf(stderr, "pdx watch wake-settings set: 有人先改了（現在是 revision %d），沒有寫入；請重跑\n", c.Current.Revision)
		return wakeExitConflict
	case http.StatusBadRequest:
		var e struct {
			Error   string `json:"error"`
			Message string `json:"message"`
			Fields  []struct {
				Path, Code, Message string
			} `json:"fields"`
		}
		_ = json.Unmarshal(res.body, &e)
		if len(e.Fields) == 0 {
			fmt.Fprintf(stderr, "pdx watch wake-settings set: 被拒絕：%s %s\n", sanitizeCell(e.Error), sanitizeCell(e.Message))
			return wakeExitInvalid
		}
		fmt.Fprintf(stderr, "pdx watch wake-settings set: 被拒絕，沒有寫入（%d 項）：\n", len(e.Fields))
		for _, f := range e.Fields {
			fmt.Fprintf(stderr, "  %s  %s  %s\n", sanitizeCell(f.Path), sanitizeCell(f.Code), sanitizeCell(f.Message))
		}
		return wakeExitInvalid
	}
	return wakeFailure("pdx watch wake-settings set", res.status, res.body, stderr)
}
