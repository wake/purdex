package main

// `pdx watch config [get [key]] | set key=value…` (host-load-control spec §12.2): this host's `watch` setting through the
// daemon's API (the App's settings page uses the same route). The daemon validates; this only reads, edits and writes back.

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/wake/purdex/internal/config"
)

// watchConfigKeys are the keys of the setting, in the order they are shown.
var watchConfigKeys = []string{"enabled", "x_cores", "y_mb", "t_s", "pressure_level", "pressure_t_s", "page_rate", "page_t_s",
	"targets", "mode", "patrol_idle_min", "auto_wake", "agent_command", "workdir", "start_prompt", "wake_prompt"}

func watchConfig(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("watch config", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "config.toml (default ~/.config/pdx/config.toml)")
	asJSON := fs.Bool("json", false, "print JSON")
	rest, err := parseInterspersed(fs, args)
	if err != nil {
		return 2
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "pdx watch config: %v\n", err)
		return 1
	}
	base := watchBase(cfg)
	items, rev, code := fetchWatchItems(base, cfg.Token, stderr)
	if code != 0 {
		return code
	}
	mode := "get"
	if len(rest) > 0 && (rest[0] == "get" || rest[0] == "set") {
		mode, rest = rest[0], rest[1:]
	}
	if mode == "get" {
		if len(rest) > 1 {
			fmt.Fprint(stderr, watchUsage)
			return 2
		}
		if len(rest) == 1 {
			v, ok := items[rest[0]]
			if !ok {
				fmt.Fprintf(stderr, "pdx watch config: unknown key %q\n", rest[0])
				return 2
			}
			fmt.Fprintln(stdout, watchValueText(v))
			return 0
		}
		if *asJSON {
			b, _ := json.MarshalIndent(items, "", "  ")
			fmt.Fprintln(stdout, string(b))
			return 0
		}
		for _, k := range watchConfigKeys {
			fmt.Fprintf(stdout, "%s = %s\n", k, watchValueText(items[k]))
		}
		return 0
	}
	if len(rest) == 0 {
		fmt.Fprint(stderr, watchUsage)
		return 2
	}
	for _, kv := range rest {
		k, v, ok := strings.Cut(kv, "=")
		cur, known := items[k]
		if !ok || !known {
			fmt.Fprintf(stderr, "pdx watch config: %q is not key=value of a known key (%s)\n", kv, strings.Join(watchConfigKeys, ", "))
			return 2
		}
		nv, err := parseWatchValue(cur, v)
		if err != nil {
			fmt.Fprintf(stderr, "pdx watch config: %s: %v\n", k, err)
			return 2
		}
		items[k] = nv
	}
	body, _ := json.Marshal(map[string]any{"items": items, "baseRevision": rev})
	res, err := doPeersRequest(http.MethodPut, base+"/api/hostconfig/watch", body, cfg.Token, peersRequestTimeout)
	if err != nil {
		return reportPeersTransportErr(err, stderr)
	}
	if res.status < 200 || res.status >= 300 {
		fmt.Fprintf(stderr, "pdx watch config: the daemon refused (%d): %s\n", res.status, strings.TrimSpace(string(res.body)))
		return 1
	}
	fmt.Fprintln(stdout, "ok")
	return 0
}

// fetchWatchItems reads the `watch` row of GET /api/hostconfig: its items and revision.
func fetchWatchItems(base, token string, stderr io.Writer) (map[string]any, int64, int) {
	res, err := doPeersRequest(http.MethodGet, base+"/api/hostconfig", nil, token, peersRequestTimeout)
	if err != nil {
		return nil, 0, reportPeersTransportErr(err, stderr)
	}
	if res.status != http.StatusOK {
		fmt.Fprintf(stderr, "pdx watch config: the daemon answered %d\n", res.status)
		return nil, 0, 1
	}
	var all struct {
		Watch struct {
			Items    map[string]any `json:"items"`
			Revision int64          `json:"revision"`
			Invalid  bool           `json:"invalid"`
		} `json:"watch"`
	}
	dec := json.NewDecoder(bytes.NewReader(res.body))
	if err := dec.Decode(&all); err != nil || all.Watch.Items == nil {
		fmt.Fprintln(stderr, "pdx watch config: this daemon has no watch setting (older version?)")
		return nil, 0, 1
	}
	if all.Watch.Invalid {
		fmt.Fprintln(stderr, "pdx watch config: the stored value is invalid; the defaults are shown, and a set replaces it")
	}
	return all.Watch.Items, all.Watch.Revision, 0
}

// watchValueText is a value as one line: lists comma-separated, strings as they are.
func watchValueText(v any) string {
	switch x := v.(type) {
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = fmt.Sprint(e)
		}
		return strings.Join(parts, ",")
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case string:
		return strings.NewReplacer("\n", `\n`, "\t", `\t`).Replace(x)
	default:
		return fmt.Sprint(v)
	}
}

// parseWatchValue reads text as the type of the current value (bool, number, list of strings, string). A string is taken as typed:
// the daemon refuses a control character in the command and the prompts (they are typed into a shell), so \n is not turned into
// a newline.
func parseWatchValue(cur any, s string) (any, error) {
	switch cur.(type) {
	case bool:
		b, err := strconv.ParseBool(s)
		if err != nil {
			return nil, fmt.Errorf("%q is not true or false", s)
		}
		return b, nil
	case float64:
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a number", s)
		}
		return f, nil
	case []any:
		if s == "" {
			return []any{}, nil
		}
		var out []any
		for _, p := range strings.Split(s, ",") {
			out = append(out, strings.TrimSpace(p))
		}
		return out, nil
	default:
		return s, nil
	}
}

// parseInterspersed parses fs's flags wherever they stand among the words (`config get key --json`) and returns the words.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var words []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return words, nil
		}
		words = append(words, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// watchBase is how the command finds the daemon (a seam: a test points it at its fake).
var watchBase = watchDaemonBase

// watchDaemonBase is the URL of this host's daemon: a wildcard bind is reached on the loopback, an IPv6 address is bracketed.
func watchDaemonBase(cfg config.Config) string {
	return "http://" + net.JoinHostPort(resolveDaemonHost(strings.Trim(cfg.Bind, "[]")), strconv.Itoa(cfg.Port))
}
