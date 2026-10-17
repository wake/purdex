package main

// `pdx assist …` and `pdx gate hosts` (assist-dispatch plan X1a): the switches, the allowed list and the hosts, through the local
// daemon's admin-only routes (the admin token from config.toml, as `pdx watch start` does).

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/wake/purdex/internal/config"
)

const assistUsage = `usage:
  pdx assist [status] [--json]
  pdx assist host add|set <alias> [--ssh <alias>] [--scripts <dir>] [--slots n] [--load-limit x] [--idle-min n] [--hold-min n]
                                  [--can <command words>]... [--cannot <command words>]... [--clear-can] [--clear-cannot]
  pdx assist host remove <alias>
  pdx assist on|off <host> --reason "<line>"
  pdx assist reserve on|off [--for 2h] [--reason "<line>"]   keep mlab free for a job (on needs --reason; at most 12h)
  pdx assist allow (--team <id|name|lead address> | --session <ref>) [--host <host>] --reason "<line>"
  pdx assist deny  (--team <id|name|lead address> | --session <ref>) [--host <host>]
  pdx gate hosts [--session <id>] [--json]
  every command takes --config <config.toml>
`

// assistTimeout covers a switch: the daemon waits for the script (on 120 s) and answers after it.
const assistTimeout = 150 * time.Second

func runAssist(args []string) { os.Exit(assistMain(args, os.Stdout, os.Stderr)) }

// assistBy is who is asking: the session of the environment, else the person.
func assistBy() string {
	for _, k := range []string{"CLAUDE_CODE_SESSION_ID", "CLAUDE_SESSION_ID"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return "person"
}

func assistMain(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return assistStatus(args, stdout, stderr)
	}
	switch args[0] {
	case "status":
		return assistStatus(args[1:], stdout, stderr)
	case "host":
		return assistHost(args[1:], stdout, stderr)
	case "on", "off":
		return assistSwitch(args[0] == "on", args[1:], stdout, stderr)
	case "reserve":
		return assistReserve(args[1:], stdout, stderr)
	case "allow", "deny":
		return assistAllow(args[0] == "allow", args[1:], stdout, stderr)
	}
	fmt.Fprintf(stderr, "pdx assist: unknown command %q\n%s", args[0], assistUsage)
	return 2
}

// assistCall sends one request to the daemon and returns the body of a 2xx answer; otherwise it has printed why.
func assistCall(cfgPath, method, path string, body any, stderr io.Writer) ([]byte, int) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "pdx assist: %v\n", err)
		return nil, 1
	}
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	res, err := doPeersRequest(method, watchBase(cfg)+path, payload, cfg.Token, assistTimeout)
	if err != nil {
		return nil, reportPeersTransportErr(err, stderr)
	}
	if res.status == http.StatusNotFound && strings.TrimSpace(string(res.body)) == "404 page not found" {
		fmt.Fprintln(stderr, "pdx assist: this daemon has no assist support (older version?)")
		return nil, 1
	}
	if res.status < 200 || res.status > 299 {
		var e struct{ Error, Detail string }
		if json.Unmarshal(res.body, &e) == nil && e.Error != "" {
			msg := e.Error
			if e.Detail != "" {
				msg += ": " + e.Detail
			}
			fmt.Fprintf(stderr, "pdx assist: %s\n", sanitizeCell(msg))
		} else {
			fmt.Fprintf(stderr, "pdx assist: %s\n", sanitizeCell(extractPeersErrorMessage(res.body)))
		}
		return nil, 1
	}
	if !json.Valid(res.body) {
		fmt.Fprintln(stderr, "pdx assist: the daemon's answer is not JSON (version mismatch or a cut-off answer)")
		return nil, 1
	}
	return res.body, 0
}

type assistCommon struct {
	fs      *flag.FlagSet
	cfgPath *string
	asJSON  *bool
}

func newAssistFlags(name string, stderr io.Writer, withJSON bool) assistCommon {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	c := assistCommon{fs: fs, cfgPath: fs.String("config", "", "config.toml (default ~/.config/pdx/config.toml)")}
	if withJSON {
		c.asJSON = fs.Bool("json", false, "print the daemon's answer")
	}
	return c
}

// parseMixed parses flags that may stand before or after the positional words (Go's flag stops at the first one) and returns
// the positional words in order.
func parseMixed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// firstWord splits the first positional word off.
func firstWord(pos []string) (string, []string) {
	if len(pos) == 0 {
		return "", nil
	}
	return pos[0], pos[1:]
}

func assistStatus(args []string, stdout, stderr io.Writer) int {
	c := newAssistFlags("assist status", stderr, true)
	pos, err := parseMixed(c.fs, args)
	if err != nil {
		return 2
	}
	if len(pos) > 0 {
		fmt.Fprint(stderr, assistUsage)
		return 2
	}
	body, rc := assistCall(*c.cfgPath, http.MethodGet, "/api/assist", nil, stderr)
	if rc != 0 {
		return rc
	}
	if *c.asJSON {
		fmt.Fprintln(stdout, strings.TrimSpace(string(body)))
		return 0
	}
	fmt.Fprint(stdout, renderAssistStatus(body))
	return 0
}

type assistStatusDoc struct {
	File struct {
		Hosts map[string]struct {
			SSH, Scripts string
			On           bool
			Gen          int
			Slots        int
			Can, Cannot  []string
			LoadLimit    float64
			IdleMin      int
			HoldMin      int
			Op           *struct{ Kind, ID string }
			By, Reason   string
		}
		Allowed  []struct{ Kind, ID, Name, Host, By, Reason string }
		Reserved *struct {
			By, Reason string
			Until      time.Time
		}
	} `json:"file"`
	Ready map[string]struct {
		Gen    int       `json:"gen"`
		At     time.Time `json:"at"`
		Err    string    `json:"err"`
		Answer struct {
			Switch string `json:"switch"`
			Repos  map[string]struct {
				Ready bool   `json:"ready"`
				Why   string `json:"why"`
			} `json:"repos"`
		} `json:"answer"`
	} `json:"ready"`
	Now         time.Time `json:"now"`
	AssistError string    `json:"assist_error"`
}

func renderAssistStatus(body []byte) string {
	var d assistStatusDoc
	if err := json.Unmarshal(body, &d); err != nil {
		return "pdx assist: unreadable answer\n"
	}
	var b strings.Builder
	if d.AssistError != "" {
		fmt.Fprintf(&b, "⚠ %s（沒有任何主機可用）\n", sanitizeCell(d.AssistError))
	}
	if len(d.File.Hosts) == 0 {
		b.WriteString("沒有設定任何輔測主機（pdx assist host add）\n")
	}
	aliases := make([]string, 0, len(d.File.Hosts))
	for a := range d.File.Hosts {
		aliases = append(aliases, a)
	}
	sort.Strings(aliases)
	for _, a := range aliases {
		h := d.File.Hosts[a]
		sw := "關"
		if h.On {
			sw = "開"
		}
		fmt.Fprintf(&b, "%s  要求：%s", sanitizeCell(a), sw)
		if h.Op != nil {
			fmt.Fprintf(&b, "（%s 進行中）", sanitizeCell(h.Op.Kind))
		}
		fmt.Fprintf(&b, "  ssh=%s  scripts=%s\n", sanitizeCell(h.SSH), sanitizeCell(h.Scripts))
		if r, ok := d.Ready[a]; ok && r.Gen == h.Gen {
			age := d.Now.Sub(r.At).Round(time.Second)
			if r.Err != "" {
				fmt.Fprintf(&b, "  檢查（%s 前）：%s\n", age, sanitizeCell(r.Err))
			} else {
				fmt.Fprintf(&b, "  檢查（%s 前）：主機說 %s\n", age, sanitizeCell(r.Answer.Switch))
				names := make([]string, 0, len(r.Answer.Repos))
				for n := range r.Answer.Repos {
					names = append(names, n)
				}
				sort.Strings(names)
				for _, n := range names {
					if rp := r.Answer.Repos[n]; rp.Ready {
						fmt.Fprintf(&b, "    %s 就緒\n", sanitizeCell(n))
					} else {
						fmt.Fprintf(&b, "    %s 未就緒：%s\n", sanitizeCell(n), sanitizeCell(rp.Why))
					}
				}
			}
		} else {
			b.WriteString("  檢查：尚無（這一代的設定還沒檢查過）\n")
		}
		if len(h.Can) > 0 || len(h.Cannot) > 0 {
			fmt.Fprintf(&b, "  can=%s cannot=%s\n", sanitizeCell(fmt.Sprintf("%q", h.Can)), sanitizeCell(fmt.Sprintf("%q", h.Cannot)))
		}
	}
	if rv := d.File.Reserved; rv != nil && rv.Until.After(d.Now) {
		fmt.Fprintf(&b, "mlab 保留中（%s）至 %s — %s\n", sanitizeCell(rv.By), rv.Until.Local().Format("15:04"), sanitizeCell(rv.Reason))
	}
	if len(d.File.Allowed) == 0 {
		b.WriteString("允許名單：空\n")
	} else {
		b.WriteString("允許名單：\n")
		for _, a := range d.File.Allowed {
			host := a.Host
			if host == "" {
				host = "全部主機"
			}
			fmt.Fprintf(&b, "  %s %s（%s）%s — %s\n", sanitizeCell(a.Kind), sanitizeCell(a.ID), sanitizeCell(a.Name), sanitizeCell(host), sanitizeCell(a.Reason))
		}
	}
	return b.String()
}

func assistHost(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, assistUsage)
		return 2
	}
	verb, rest := args[0], args[1:]
	if verb != "add" && verb != "set" && verb != "remove" {
		fmt.Fprintf(stderr, "pdx assist host: unknown command %q\n%s", verb, assistUsage)
		return 2
	}
	c := newAssistFlags("assist host "+verb, stderr, false)
	ssh := c.fs.String("ssh", "", "ssh alias the scripts run against")
	scripts := c.fs.String("scripts", "", "directory of assist-on / assist-off / assist-check")
	slots := c.fs.Int("slots", 0, "dispatch slots mlab keeps for the host")
	loadLimit := c.fs.Float64("load-limit", 0, "load per core above which nothing is sent")
	idleMin := c.fs.Int("idle-min", 0, "CPU idle % below which nothing is sent")
	holdMin := c.fs.Int("hold-min", 0, "minutes a host is held off after going over a limit")
	var can, cannot stringList
	c.fs.Var(&can, "can", "command words the host can run (repeatable)")
	c.fs.Var(&cannot, "cannot", "command words the host cannot run (repeatable)")
	clearCan := c.fs.Bool("clear-can", false, "empty the can list")
	clearCannot := c.fs.Bool("clear-cannot", false, "empty the cannot list")
	pos, err := parseMixed(c.fs, rest)
	if err != nil {
		return 2
	}
	alias, pos := firstWord(pos)
	if alias == "" || len(pos) > 0 {
		fmt.Fprint(stderr, assistUsage)
		return 2
	}
	if verb == "remove" {
		body, rc := assistCall(*c.cfgPath, http.MethodDelete, "/api/assist/host/"+url.PathEscape(alias), nil, stderr)
		if rc == 0 {
			fmt.Fprintf(stdout, "已移除 %s\n", alias)
		}
		_ = body
		return rc
	}
	req := map[string]any{"alias": alias}
	c.fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "ssh":
			req["ssh"] = *ssh
		case "scripts":
			req["scripts"] = *scripts
		case "slots":
			req["slots"] = *slots
		case "load-limit":
			req["load_limit"] = *loadLimit
		case "idle-min":
			req["idle_min"] = *idleMin
		case "hold-min":
			req["hold_min"] = *holdMin
		case "can":
			req["can"] = []string(can)
		case "cannot":
			req["cannot"] = []string(cannot)
		}
	})
	if *clearCan {
		req["clear_can"] = true
	}
	if *clearCannot {
		req["clear_cannot"] = true
	}
	body, rc := assistCall(*c.cfgPath, http.MethodPost, "/api/assist/host", req, stderr)
	if rc != 0 {
		return rc
	}
	var r struct{ Result string }
	_ = json.Unmarshal(body, &r)
	fmt.Fprintf(stdout, "%s：%s\n", sanitizeCell(alias), map[string]string{"added": "已新增", "changed": "已更新", "unchanged": "沒有變動"}[r.Result])
	return 0
}

func assistSwitch(on bool, args []string, stdout, stderr io.Writer) int {
	rest := args
	c := newAssistFlags("assist switch", stderr, false)
	reason := c.fs.String("reason", "", "why (one line, required)")
	pos, err := parseMixed(c.fs, rest)
	if err != nil {
		return 2
	}
	host, pos := firstWord(pos)
	if host == "" || len(pos) > 0 {
		fmt.Fprint(stderr, assistUsage)
		return 2
	}
	if *reason == "" {
		fmt.Fprintln(stderr, "pdx assist: --reason is required")
		return 2
	}
	body, rc := assistCall(*c.cfgPath, http.MethodPost, "/api/assist/switch", map[string]any{"host": host, "on": on, "reason": *reason, "by": assistBy()}, stderr)
	if rc != 0 {
		return rc
	}
	var r struct{ Detail string }
	_ = json.Unmarshal(body, &r)
	state := "關"
	if on {
		state = "開"
	}
	fmt.Fprintf(stdout, "%s 輔測已%s：%s\n", sanitizeCell(host), state, sanitizeCell(r.Detail))
	return 0
}

func assistAllow(allow bool, args []string, stdout, stderr io.Writer) int {
	c := newAssistFlags("assist allow", stderr, false)
	teamArg := c.fs.String("team", "", "a team: its id, a name, or its lead's address")
	sess := c.fs.String("session", "", "a session: its ref")
	host := c.fs.String("host", "", "only for this host (default every host)")
	reason := c.fs.String("reason", "", "why (one line)")
	pos, err := parseMixed(c.fs, args)
	if err != nil {
		return 2
	}
	if (*teamArg == "") == (*sess == "") || len(pos) > 0 {
		fmt.Fprintf(stderr, "pdx assist: name exactly one of --team and --session\n%s", assistUsage)
		return 2
	}
	if allow && *reason == "" {
		fmt.Fprintln(stderr, "pdx assist: --reason is required")
		return 2
	}
	kind, id := "team", *teamArg
	if *sess != "" {
		kind, id = "session", *sess
	}
	path := "/api/assist/deny"
	if allow {
		path = "/api/assist/allow"
	}
	body, rc := assistCall(*c.cfgPath, http.MethodPost, path, map[string]any{"kind": kind, "id": id, "host": *host, "reason": *reason, "by": assistBy()}, stderr)
	if rc != 0 {
		return rc
	}
	var r struct{ Result, ID, Name string }
	_ = json.Unmarshal(body, &r)
	switch r.Result {
	case "already":
		fmt.Fprintf(stdout, "%s %s（%s）已在名單上\n", kind, sanitizeCell(r.ID), sanitizeCell(r.Name))
	case "allowed":
		fmt.Fprintf(stdout, "已允許 %s %s（%s）\n", kind, sanitizeCell(r.ID), sanitizeCell(r.Name))
	default:
		fmt.Fprintf(stdout, "已從名單移除 %s %s\n", kind, sanitizeCell(r.ID))
	}
	return 0
}

// gateHosts is `pdx gate hosts`: for this session and the repo of the current directory, each assist host and whether it can be used.
func gateHosts(args []string, stdout, stderr io.Writer) int {
	c := newAssistFlags("gate hosts", stderr, true)
	session := c.fs.String("session", "", "session id (default: the one of the environment)")
	pos, err := parseMixed(c.fs, args)
	if err != nil {
		return 2
	}
	if len(pos) > 0 {
		fmt.Fprint(stderr, assistUsage)
		return 2
	}
	sid := *session
	if sid == "" {
		sid = assistBy()
		if sid == "person" {
			sid = ""
		}
	}
	cwd, _ := os.Getwd()
	q := url.Values{"session": {sid}, "cwd": {cwd}}
	body, rc := assistCall(*c.cfgPath, http.MethodGet, "/api/assist/hosts?"+q.Encode(), nil, stderr)
	if rc != 0 {
		return rc
	}
	if *c.asJSON {
		fmt.Fprintln(stdout, strings.TrimSpace(string(body)))
		return 0
	}
	var d struct {
		Repo  string
		Hosts []struct {
			Host                       string
			On, Ready, Allowed, Usable bool
			Why                        string
		}
	}
	if err := json.Unmarshal(body, &d); err != nil {
		fmt.Fprintln(stderr, "pdx gate hosts: unreadable answer")
		return 1
	}
	repo := d.Repo
	if repo == "" {
		repo = "（不在 git 倉庫內）"
	}
	fmt.Fprintf(stdout, "倉庫：%s\nmlab        可用（本機）\n", sanitizeCell(repo))
	if len(d.Hosts) == 0 {
		fmt.Fprintln(stdout, "no assist hosts on this host")
		return 0
	}
	for _, h := range d.Hosts {
		switch {
		case h.Usable:
			fmt.Fprintf(stdout, "%-11s 可用\n", sanitizeCell(h.Host))
		default:
			fmt.Fprintf(stdout, "%-11s 不可用：%s\n", sanitizeCell(h.Host), sanitizeCell(h.Why))
		}
	}
	return 0
}

func assistReserve(args []string, stdout, stderr io.Writer) int {
	rest := args
	c := newAssistFlags("assist reserve", stderr, false)
	forDur := c.fs.String("for", "", "how long, such as 2h (default 2h, at most 12h)")
	reason := c.fs.String("reason", "", "why (one line, required for on)")
	pos, err := parseMixed(c.fs, rest)
	if err != nil {
		return 2
	}
	word, pos := firstWord(pos)
	if (word != "on" && word != "off") || len(pos) > 0 {
		fmt.Fprint(stderr, assistUsage)
		return 2
	}
	if word == "on" && *reason == "" {
		fmt.Fprintln(stderr, "pdx assist: --reason is required")
		return 2
	}
	req := map[string]any{"on": word == "on", "reason": *reason, "by": assistBy()}
	if *forDur != "" {
		req["for"] = *forDur
	}
	body, rc := assistCall(*c.cfgPath, http.MethodPost, "/api/assist/reserve", req, stderr)
	if rc != 0 {
		return rc
	}
	var r struct{ Until time.Time }
	_ = json.Unmarshal(body, &r)
	if word == "on" {
		fmt.Fprintf(stdout, "mlab 已保留至 %s\n", r.Until.Local().Format("15:04"))
	} else {
		fmt.Fprintln(stdout, "已解除 mlab 保留")
	}
	return 0
}
