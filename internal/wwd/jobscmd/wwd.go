package jobscmd

import (
	"context"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/wake/purdex/internal/wwd/testcmd"
	"github.com/wake/purdex/internal/wwd/wire"
)

const WWDUsage = `usage: pdx wwd [status] [--json]
       pdx wwd pool [--json]
       pdx wwd worker on|off [--heavy N] [--light N] [--disk-min-gb GB]
       pdx wwd exception start <name> (--until <30m|RFC3339> | --pid <pid>) [--holds heavy,light]
       pdx wwd exception stop <name> [--host <host>]
       pdx wwd hold <host> --for <dur> --reason <text> [--allow-class release|merge|dev]... [--allow-repo <repo>]... [--release] [--expect pressure]
       pdx wwd usage [--since <6h|7d|RFC3339> [--after <job id>]] [--limit N] [--json]
       pdx wwd labels [--json]
       pdx wwd quarantine <task:repo/task>
       pdx wwd release <task:repo/task>
       pdx wwd qualify <repo>/<task> | --status [--json]
  labels    the task labels this host earned (or lost to a quarantine), with the fingerprint each was earned under
  quarantine  stop advertising a task label this host earned, by hand (it is advertised again after qualifying anew)
  release     let go of a task label this host earned or lost to a quarantine: the row is deleted
  qualify     this host earns a task label by running the task and the reference host's run of it on one snapshot and comparing them; --status lists the pairs
  status    this host's wwd: version, labels, worker switch and slots
  pool       every host the pool knows: reachable, pressure zone, free slots, worker
  worker     switch this host's worker; --heavy / --light set the slot counts; --disk-min-gb the free GB the work volume must keep for a heavy run (0: none)
  exception  tell wwd a dev server or packaging run is using the host: its worker takes no new heavy work (and no light work if
             --holds says light) until --until passes or --pid ends
  hold       keep a host quiet for a while (the worker takes no new heavy work there): an exception with a reason. With --allow-class or
             --allow-repo the worker still takes the jobs of those classes or repos, and only those; --release is a release (the host's
             light slots drop to 1 while it lasts); --expect pressure is a deliberate stress test (the PSI part of the mserv alert waits).
             The command waits until the hold shows in that host's facts (30 s) and fails if it does not: the worker decides from the
             facts. End it with: pdx wwd exception stop <name> --host <host>.
  usage      what this host's finished jobs used (CPU, cores, memory) and what the host was like around them; kept 30 days
`

// WWD is `pdx wwd`; it returns the exit code.
func WWD(ctx context.Context, args []string, env Env) int {
	sub := "status"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "status":
		return wwdStatus(ctx, args, env)
	case "pool":
		return wwdPool(ctx, args, env)
	case "worker":
		return wwdWorker(ctx, args, env)
	case "exception":
		return wwdException(ctx, args, env)
	case "hold":
		return wwdHold(ctx, args, env)
	case "usage":
		return wwdUsage(ctx, args, env)
	case "labels":
		return wwdLabels(ctx, args, env)
	case "quarantine":
		return wwdQuarantine(ctx, args, env)
	case "release":
		return wwdRelease(ctx, args, env)
	case "qualify":
		return wwdQualify(ctx, args, env)
	case "help":
		io.WriteString(env.Stdout, WWDUsage)
		return ExitOK
	}
	fmt.Fprintf(env.Stderr, "pdx wwd: unknown command %q\n\n%s", sub, WWDUsage)
	return ExitUsage
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func wwdStatus(ctx context.Context, args []string, env Env) int {
	flags, pos, err := splitFlags(args, "json")
	if err == nil {
		err = only(flags, "json")
	}
	if err == nil && len(pos) > 0 {
		err = fmt.Errorf("unexpected argument %q", pos[0])
	}
	if err != nil {
		return usageErr(env, WWDUsage, err)
	}
	info, err := env.API.Info(ctx)
	if err != nil {
		return fail(env, "wwd status", err)
	}
	w, err := env.API.Worker(ctx)
	if err != nil {
		return fail(env, "wwd status", err)
	}
	if flags["json"] != "" {
		writeJSON(env.Stdout, map[string]any{"info": info, "worker": w})
		return ExitOK
	}
	fmt.Fprintf(env.Stdout, "wwd %s  %s  (%s/%s, proto %d)\n", testcmd.CleanLine(info.Alias), testcmd.CleanLine(info.Version), testcmd.CleanLine(info.Platform), testcmd.CleanLine(info.Arch), info.Proto)
	rule := w.OwnerRule
	if rule == "" {
		rule = "none"
	}
	fmt.Fprintf(env.Stdout, "worker %s  slots heavy %d light %d  owner rule: %s%s\n", onOff(w.Enabled), w.Slots.Heavy, w.Slots.Light, testcmd.CleanLine(rule), diskFloorText(w))
	fmt.Fprintf(env.Stdout, "labels %s\n", testcmd.CleanLine(strings.Join(info.Labels, " ")))
	return ExitOK
}

func wwdPool(ctx context.Context, args []string, env Env) int {
	flags, pos, err := splitFlags(args, "json")
	if err == nil {
		err = only(flags, "json")
	}
	if err == nil && len(pos) > 0 {
		err = fmt.Errorf("unexpected argument %q", pos[0])
	}
	if err != nil {
		return usageErr(env, WWDUsage, err)
	}
	pool, err := env.API.Pool(ctx)
	if err != nil {
		return fail(env, "wwd pool", err)
	}
	if flags["json"] != "" {
		writeJSON(env.Stdout, pool)
		return ExitOK
	}
	tw := tabwriter.NewWriter(env.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "HOST\tREACHABLE\tZONE\tFREE heavy/light\tWORKER\tRUNNING heavy/light\tFACTS AGE")
	for _, p := range pool.Peers {
		zone, free, worker, running, factsAge := "-", "-", onOff(p.Info.Worker.Enabled), "-", "-"
		if f := p.Facts; f != nil {
			zone = string(f.Derived.Zone)
			free = fmt.Sprintf("%d/%d", f.Derived.Headroom.Heavy, f.Derived.Headroom.Light)
			worker = onOff(f.Worker.Enabled)
			running = fmt.Sprintf("%d/%d", f.Worker.Running.Heavy, f.Worker.Running.Light)
			factsAge = age(env.now(), f.At)
		}
		reach := "yes"
		if !p.Reachable {
			reach = "NO"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", testcmd.CleanLine(p.Info.Alias), reach, zone, free, worker, running, factsAge)
	}
	tw.Flush()
	return ExitOK
}

// diskFloorText is "  disk floor 10 GB" (or "none"), "" for settings that carry no floor (a wwd from before it).
func diskFloorText(w wire.WorkerSettings) string {
	switch {
	case w.DiskMinGB == nil:
		return ""
	case *w.DiskMinGB == 0:
		return "  disk floor none"
	}
	return "  disk floor " + strconv.FormatFloat(*w.DiskMinGB, 'f', -1, 64) + " GB"
}

func wwdWorker(ctx context.Context, args []string, env Env) int {
	flags, pos, err := splitFlags(args)
	if err == nil {
		err = only(flags, "heavy", "light", "disk-min-gb")
	}
	var floor *float64
	if v, ok := flags["disk-min-gb"]; ok && err == nil {
		n, e := strconv.ParseFloat(v, 64)
		if e != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 {
			err = fmt.Errorf("--disk-min-gb must be a number of GB, 0 (no floor) or more")
		}
		floor = &n
	}
	if err == nil && (len(pos) != 1 || (pos[0] != "on" && pos[0] != "off")) {
		err = fmt.Errorf("worker takes on or off")
	}
	slots := map[string]int{}
	for _, k := range []string{"heavy", "light"} {
		if v, ok := flags[k]; ok && err == nil {
			n, e := strconv.Atoi(v)
			if e != nil || n < 0 {
				err = fmt.Errorf("--%s must be a number of slots, 0 or more", k)
			}
			slots[k] = n
		}
	}
	if err != nil {
		return usageErr(env, WWDUsage, err)
	}
	cur, err := env.API.Worker(ctx)
	if err != nil {
		return fail(env, "wwd worker", err)
	}
	cur.Enabled = pos[0] == "on"
	if n, ok := slots["heavy"]; ok {
		cur.Slots.Heavy = n
	}
	if n, ok := slots["light"]; ok {
		cur.Slots.Light = n
	}
	if floor != nil {
		cur.DiskMinGB = floor
	}
	now, err := env.API.SetWorker(ctx, cur)
	if err != nil {
		return fail(env, "wwd worker", err)
	}
	fmt.Fprintf(env.Stdout, "worker %s  slots heavy %d light %d%s\n", onOff(now.Enabled), now.Slots.Heavy, now.Slots.Light, diskFloorText(now))
	return ExitOK
}

func wwdException(ctx context.Context, args []string, env Env) int {
	if len(args) == 0 || (args[0] != "start" && args[0] != "stop") {
		return usageErr(env, WWDUsage, fmt.Errorf("exception takes start or stop"))
	}
	action := args[0]
	flags, pos, err := splitFlags(args[1:])
	if err == nil && len(pos) != 1 {
		err = fmt.Errorf("an exception name is needed")
	}
	if err != nil {
		return usageErr(env, WWDUsage, err)
	}
	name := pos[0]
	if action == "stop" {
		if err := only(flags, "host"); err != nil {
			return usageErr(env, WWDUsage, err)
		}
		host := flags["host"]
		if host != "" && !wire.ValidAlias(host) {
			return usageErr(env, WWDUsage, fmt.Errorf("--host %q is not a host alias", host))
		}
		if err := env.API.StopExceptionOn(ctx, host, name); err != nil {
			return fail(env, "wwd exception", err)
		}
		fmt.Fprintf(env.Stdout, "exception %s stopped\n", name)
		return ExitOK
	}
	if err := only(flags, "until", "pid", "holds"); err != nil {
		return usageErr(env, WWDUsage, err)
	}
	e := wire.Exception{Name: name}
	if v := flags["until"]; v != "" {
		t, err := parseUntil(v, env.now())
		if err != nil {
			return usageErr(env, WWDUsage, err)
		}
		e.Until = &t
	}
	if v := flags["pid"]; v != "" {
		pid, err := strconv.Atoi(v)
		if err != nil || pid <= 0 {
			return usageErr(env, WWDUsage, fmt.Errorf("--pid must be a process id"))
		}
		e.PID = &pid
	}
	holds := flags["holds"]
	if holds == "" {
		holds = "heavy,light"
	}
	for _, h := range strings.Split(holds, ",") {
		e.Holds = append(e.Holds, wire.Hold(strings.TrimSpace(h)))
	}
	if err := e.Validate(); err != nil {
		return usageErr(env, WWDUsage, err)
	}
	if err := env.API.StartException(ctx, e); err != nil {
		return fail(env, "wwd exception", err)
	}
	fmt.Fprintf(env.Stdout, "exception %s started\n", name)
	return ExitOK
}

// parseUntil reads --until: a duration from now (30m, 2h) or an RFC 3339 time.
func parseUntil(v string, now time.Time) (time.Time, error) {
	if d, err := time.ParseDuration(v); err == nil {
		if d <= 0 {
			return time.Time{}, fmt.Errorf("--until %q is not in the future", v)
		}
		return now.Add(d).UTC(), nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, fmt.Errorf("--until must be a duration like 30m or an RFC 3339 time, got %q", v)
	}
	if !t.After(now) {
		return time.Time{}, fmt.Errorf("--until %q is not in the future", v)
	}
	return t.UTC(), nil
}
