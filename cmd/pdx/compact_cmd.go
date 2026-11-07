package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	"github.com/wake/purdex/internal/team"
)

const compactUsage = "usage: pdx compact <ref> [--auto on|off] [--threshold N] [--idle on|off] [--reset | --now] [--config <path>]\n" +
	"       (no flag: show the member's compaction settings; --reset clears the override and takes no other flag; N is 1-100;\n" +
	"        --now compacts the member now, or when its running turn ends, and takes no other flag)\n" +
	"       (<ref> is _xxxxxx, or an address from pdx team; only a member of your own team, on this host)"

func runCompact(args []string) {
	os.Exit(runCompactCmd(context.Background(), args, os.Getenv, os.Stdout, os.Stderr))
}

// onOff parses a given on|off flag value.
func onOff(v string) (*bool, error) {
	switch v {
	case "on":
		t := true
		return &t, nil
	case "off":
		f := false
		return &f, nil
	}
	return nil, fmt.Errorf("值要是 on 或 off，不是 %q", v)
}

// runCompactCmd implements `pdx compact <ref> [...]`: a lead reads or changes one member's compaction override. The target goes
// as typed. Not Idempotent: a replay of a set is harmless but a reset after a set is not the same call.
func runCompactCmd(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer, clientOpts ...daemonclient.Option) int {
	fs := flag.NewFlagSet("pdx compact", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "")
	autoV, idleV := fs.String("auto", "", ""), fs.String("idle", "", "")
	thresholdV := fs.String("threshold", "", "")
	reset := fs.Bool("reset", false, "")
	now := fs.Bool("now", false, "")
	pos, err := parseTeamFlags(fs, args)
	var set team.CompactionOverride
	if err == nil && (len(pos) != 1 || strings.TrimSpace(pos[0]) == "") {
		err = errors.New("需要剛好一個 <ref>（_xxxxxx 或 <host>/<name>）")
	}
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true }) // a flag that was given must carry a value, even ""
	if err == nil && given["auto"] {
		set.Auto, err = onOff(*autoV)
	}
	if err == nil && given["idle"] {
		set.Idle, err = onOff(*idleV)
	}
	if err == nil && given["threshold"] {
		n, convErr := strconv.Atoi(*thresholdV)
		if convErr != nil || n < 1 || n > 100 {
			err = fmt.Errorf("--threshold 要是 1 到 100 的整數，不是 %q", *thresholdV)
		} else {
			set.Threshold = &n
		}
	}
	changes := set.Auto != nil || set.Idle != nil || set.Threshold != nil
	if err == nil && *reset && changes {
		err = errors.New("--reset 不能和 --auto／--threshold／--idle 一起用")
	}
	if err == nil && *now && (*reset || changes) {
		err = errors.New("--now 不能和 --auto／--threshold／--idle／--reset 一起用")
	}
	if err != nil {
		fmt.Fprintf(stderr, "pdx compact: %v\n%s\n", err, compactUsage)
		return ExitUsage
	}
	req := team.MemberCompactionRequest{Target: pos[0], Reset: *reset, Now: *now}
	if changes {
		req.Set = &set
	}
	client, inbox, ok := teamSetup("compact", *cfgPath, getenv, stderr, clientOpts)
	if !ok {
		return ExitError
	}
	req.OriginInbox = inbox
	var v team.MemberCompactionView
	if _, err := client.Do(ctx, http.MethodPost, "/api/team/member-compaction", req, &v); err != nil {
		return teamReportErr("compact", err, stderr)
	}
	if *now {
		if v.Now == "turn_end" {
			fmt.Fprintln(stdout, "member 正在跑回合，這個回合結束時壓縮")
		} else {
			fmt.Fprintln(stdout, "已叫 member 壓縮")
		}
		return ExitOK
	}
	fmt.Fprintf(stdout, "member %s [%s]\n生效  %s\n覆寫  %s\n", sanitizeCell(v.Address), sanitizeCell(v.Ref), compactEffectiveText(v.Effective), compactOverrideText(v.Override))
	return ExitOK
}

func onOffText(b bool) string {
	if b {
		return "開"
	}
	return "關"
}

func compactEffectiveText(e team.CompactionEffective) string {
	if !e.Managed {
		if e.Reason == team.CompactionReasonSelfRelay {
			return "不介入（自我接力）"
		}
		return "不介入（member 由 lead 的接力設定處理）"
	}
	if e.Auto {
		return fmt.Sprintf("自動 %d%%  閒置 %s", e.Threshold, onOffText(e.Idle))
	}
	return "手動  閒置 " + onOffText(e.Idle)
}

func compactOverrideText(o team.CompactionOverride) string {
	var parts []string
	if o.Auto != nil {
		parts = append(parts, "自動 "+onOffText(*o.Auto))
	}
	if o.Threshold != nil {
		parts = append(parts, fmt.Sprintf("門檻 %d", *o.Threshold))
	}
	if o.Idle != nil {
		parts = append(parts, "閒置 "+onOffText(*o.Idle))
	}
	if len(parts) == 0 {
		return "無"
	}
	return strings.Join(parts, "  ")
}

// compactColumn is the COMPACT cell of pdx team: nil is unreadable or remote.
func compactColumn(c *team.CompactionEffective) string {
	switch {
	case c == nil:
		return "-"
	case !c.Managed:
		return "接力"
	case c.Auto:
		return fmt.Sprintf("自動 %d%%", c.Threshold)
	}
	return "手動"
}
