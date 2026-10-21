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
	"os"
	"strconv"
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
	var view wakeView
	if err := json.Unmarshal(res.body, &view); err != nil || len(view.Settings) == 0 || view.Etag == "" {
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
		if asJSON {
			fmt.Fprintln(stdout, strings.TrimSpace(string(res.body)))
			return 0
		}
		var done wakeView
		_ = json.Unmarshal(res.body, &done)
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

// A step of a path: a key of an object, then any number of [i] of an array.
type wakeStep struct {
	key string
	idx []int
}

// parseWakePath reads `rules.patrol.times[0]` the way the spec writes a path: keys with dots, and [i] after a key for an element of an array.
func parseWakePath(path string) ([]wakeStep, error) {
	var steps []wakeStep
	for _, part := range strings.Split(path, ".") {
		key, rest, _ := strings.Cut(part, "[")
		if key == "" {
			return nil, fmt.Errorf("empty name in the path")
		}
		st := wakeStep{key: key}
		for rest != "" || strings.Contains(part, "[") && st.idx == nil {
			end := strings.Index(rest, "]")
			if end < 0 {
				return nil, fmt.Errorf("[ without ]")
			}
			n, err := strconv.Atoi(rest[:end])
			if err != nil || n < 0 {
				return nil, fmt.Errorf("[%s] is not an index", rest[:end])
			}
			st.idx = append(st.idx, n)
			rest = rest[end+1:]
			if rest == "" {
				break
			}
			if !strings.HasPrefix(rest, "[") {
				return nil, fmt.Errorf("%q after ]", rest)
			}
			rest = rest[1:]
		}
		steps = append(steps, st)
	}
	return steps, nil
}

// applyWakePath sets the leaf at path in doc to the value in raw, read as the type the leaf has now (so a path that is not in the document
// is an error here, not a 400 later): bool, whole number, string (`@file` reads the file, `@@` is a literal @), or for an array a comma list.
func applyWakePath(doc map[string]any, path, raw string) error {
	steps, err := parseWakePath(path)
	if err != nil {
		return err
	}
	var cur any = doc
	for i, st := range steps {
		obj, ok := cur.(map[string]any)
		if !ok {
			return fmt.Errorf("%s is not an object", strings.Join(wakeNames(steps[:i]), "."))
		}
		child, ok := obj[st.key]
		if !ok {
			return fmt.Errorf("no such path (%s is not in the document)", st.key)
		}
		last := i == len(steps)-1
		// indexes into arrays
		for j, ix := range st.idx {
			arr, ok := child.([]any)
			if !ok {
				return fmt.Errorf("%s is not an array", st.key)
			}
			if ix >= len(arr) {
				return fmt.Errorf("index %d is past the end of %s (%d elements); set the whole array to add", ix, st.key, len(arr))
			}
			if last && j == len(st.idx)-1 {
				v, err := wakeScalar(arr[ix], raw)
				if err != nil {
					return err
				}
				arr[ix] = v
				return nil
			}
			child = arr[ix]
		}
		if last {
			v, err := wakeLeaf(child, raw)
			if err != nil {
				return err
			}
			obj[st.key] = v
			return nil
		}
		cur = child
	}
	return fmt.Errorf("empty path")
}

func wakeNames(steps []wakeStep) []string {
	out := make([]string, len(steps))
	for i, s := range steps {
		out[i] = s.key
	}
	return out
}

// wakeLeaf reads raw as the type of old.
func wakeLeaf(old any, raw string) (any, error) {
	switch old.(type) {
	case []any:
		if raw == "" {
			return []any{}, nil // an empty list, not null
		}
		parts := strings.Split(raw, ",")
		out := make([]any, len(parts))
		for i, p := range parts {
			out[i] = strings.TrimSpace(p)
		}
		return out, nil
	case map[string]any:
		return nil, fmt.Errorf("is an object: name a field of it")
	}
	return wakeScalar(old, raw)
}

// wakeScalar reads raw as the type of old, a bool, a whole number or a string.
func wakeScalar(old any, raw string) (any, error) {
	switch old.(type) {
	case bool:
		switch raw {
		case "true", "on", "yes":
			return true, nil
		case "false", "off", "no":
			return false, nil
		}
		return nil, fmt.Errorf("%q is not true or false", raw)
	case json.Number:
		n, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("%q is not a whole number", raw)
		}
		return json.Number(strconv.Itoa(n)), nil
	case string:
		if strings.HasPrefix(raw, "@@") {
			return raw[1:], nil
		}
		if strings.HasPrefix(raw, "@") {
			b, err := os.ReadFile(raw[1:])
			if err != nil {
				return nil, fmt.Errorf("cannot read the file: %v", err)
			}
			return string(b), nil
		}
		return raw, nil
	}
	return nil, fmt.Errorf("cannot be set from the command line")
}

// printWakeView is `show`: the document as a person reads it, then where it came from and how it has been working.
func printWakeView(w io.Writer, v wakeView) {
	var s struct {
		Enabled   bool `json:"enabled"`
		AutoStart bool `json:"auto_start"`
		Rules     struct {
			Patrol struct {
				Prompt      string         `json:"prompt"`
				IntervalMin map[string]int `json:"interval_min"`
				MaxGapHours int            `json:"max_gap_hours"`
				Times       []string       `json:"times"`
				TZ          string         `json:"tz"`
			} `json:"patrol"`
			DiskLow struct {
				Enabled bool   `json:"enabled"`
				Prompt  string `json:"prompt"`
				BelowGB int    `json:"below_gb"`
			} `json:"disk_low"`
			WwdAlert struct {
				Enabled  bool     `json:"enabled"`
				Prompt   string   `json:"prompt"`
				Levels   []string `json:"levels"`
				KindsOff []string `json:"kinds_off"`
			} `json:"wwd_alert"`
			Unattended struct {
				Enabled bool   `json:"enabled"`
				Prompt  string `json:"prompt"`
			} `json:"unattended_changed"`
			UserBack struct {
				Enabled bool   `json:"enabled"`
				Prompt  string `json:"prompt"`
				AwayMin int    `json:"away_min"`
			} `json:"user_back"`
		} `json:"rules"`
	}
	_ = json.Unmarshal(v.Settings, &s)
	role := "不是正本"
	if v.IsSource {
		role = "這台就是正本"
	}
	fmt.Fprintf(w, "喚醒設定  正本：%s（%s）  revision %d  etag %s\n", sanitizeCell(v.Source), role, v.Revision, sanitizeCell(v.Etag))
	fetched := "從未取到"
	if v.FetchedAt != nil {
		fetched = sanitizeCell(*v.FetchedAt)
	}
	fmt.Fprintf(w, "  stale：%s   唯讀：%s   取得時間：%s\n", wakeYesNo(v.Stale), wakeYesNo(v.Readonly), fetched)
	fmt.Fprintf(w, "  總開關 %s（關掉時巡檢以外的自動叫醒都不發生，不影響巡檢）   自動叫起 %s（同上）\n", wakeOnOff(s.Enabled), wakeOnOff(s.AutoStart))
	p, iv := s.Rules.Patrol, s.Rules.Patrol.IntervalMin
	fmt.Fprintf(w, "巡檢（不能關）\n  間隔（分鐘，0＝只剩上限）：休息 %d  在電腦前 %d  不在電腦前 %d  吃緊 %d  平穩 %d\n", iv["resting"], iv["present"], iv["away"], iv["busy"], iv["calm"])
	times := "（無）"
	if len(p.Times) > 0 {
		times = strings.Join(p.Times, " ")
	}
	fmt.Fprintf(w, "  最長不巡上限 %d 小時   固定時刻 %s   時區 %s\n  prompt：%s\n", p.MaxGapHours, times, sanitizeCell(p.TZ), wakePromptText(p.Prompt))
	fmt.Fprintf(w, "disk_low  %s  低於 %d GB 叫一次\n  prompt：%s\n", wakeOnOff(s.Rules.DiskLow.Enabled), s.Rules.DiskLow.BelowGB, wakePromptText(s.Rules.DiskLow.Prompt))
	a := s.Rules.WwdAlert
	fmt.Fprintf(w, "wwd_alert  %s  levels %s  不叫的 kind %s\n  prompt：%s\n", wakeOnOff(a.Enabled), wakeListText(a.Levels), wakeListText(a.KindsOff), wakePromptText(a.Prompt))
	fmt.Fprintf(w, "unattended_changed  %s\n  prompt：%s\n", wakeOnOff(s.Rules.Unattended.Enabled), wakePromptText(s.Rules.Unattended.Prompt))
	fmt.Fprintf(w, "user_back  %s  離開 %d 分鐘以上回來才叫\n  prompt：%s\n", wakeOnOff(s.Rules.UserBack.Enabled), s.Rules.UserBack.AwayMin, wakePromptText(s.Rules.UserBack.Prompt))
	ps := v.PatrolStatus
	fmt.Fprintf(w, "巡檢狀態\n  最近成功：%s\n", wakeWhenWhere(ps.LastOKAt, ps.LastOKHost))
	if ps.LastFailAt != nil {
		fmt.Fprintf(w, "  最近失敗：%s  %s  %s\n", sanitizeCell(*ps.LastFailAt), sanitizeCell(wakeDeref(ps.LastFailCode)), sanitizeCell(wakeDeref(ps.LastFailDetail)))
	} else {
		fmt.Fprintln(w, "  最近失敗：（無）")
	}
	if len(v.Suppressed) == 0 {
		fmt.Fprintln(w, "最近因設定而沒叫的：（無）")
		return
	}
	fmt.Fprintf(w, "最近因設定而沒叫的（%d 筆，顯示最近 %d）：\n", len(v.Suppressed), min(len(v.Suppressed), wakeSuppressedN))
	for i, x := range v.Suppressed {
		if i == wakeSuppressedN {
			break
		}
		fmt.Fprintf(w, "  %s  %s  %s  %s  %s\n", sanitizeCell(x.At), sanitizeCell(x.Host), sanitizeCell(x.Reason), sanitizeCell(x.Why), sanitizeCell(x.Detail))
	}
}

func wakeYesNo(b bool) string {
	if b {
		return "是"
	}
	return "否"
}

func wakeOnOff(b bool) string {
	if b {
		return "開"
	}
	return "關"
}

func wakePromptText(s string) string {
	if s == "" {
		return "（空）"
	}
	return sanitizeCell(s)
}

func wakeListText(l []string) string {
	if len(l) == 0 {
		return "（無）"
	}
	return sanitizeCell(strings.Join(l, ","))
}

func wakeDeref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func wakeWhenWhere(at, host *string) string {
	if at == nil {
		return "（無）"
	}
	if host != nil && *host != "" {
		return sanitizeCell(*at) + "  " + sanitizeCell(*host)
	}
	return sanitizeCell(*at)
}
