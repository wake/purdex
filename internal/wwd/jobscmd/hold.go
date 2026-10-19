package jobscmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wake/purdex/internal/wwd/client"
	"github.com/wake/purdex/internal/wwd/wire"
)

// `pdx wwd hold` (spec §13.4, WWD-8 8b-3): a line that needs a host quiet asks wwd, not an agent. The hold is an exception with the hold
// fields; it is sent to the local wwd with the host named, which forwards it (spec §10). The command then waits until the hold shows in that
// host's facts, because the worker decides from the facts, not from the POST (spec §13.4, "When a hold takes effect"): success means the
// hold is in force, a timeout is a failure even though it was saved.

const (
	holdPoll        = time.Second
	defaultHoldWait = 30 * time.Second
	maxReason       = 200
	maxSlug         = 40
)

// holdName is the exception's name, made from the reason so the same line again replaces the same hold: "hold-" and the reason's
// letters and digits (lower case, anything else becomes one "-"), at most maxSlug of them; a reason with none (Chinese, say) gets a short
// hash instead, so two such reasons are two holds.
func holdName(reason string) string {
	var b strings.Builder
	dash := true // no leading dash
	for _, r := range strings.ToLower(reason) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	slug := strings.TrimRight(b.String(), "-")
	if len(slug) > maxSlug {
		slug = strings.TrimRight(slug[:maxSlug], "-")
	}
	if slug == "" {
		sum := sha256.Sum256([]byte(strings.TrimSpace(reason)))
		slug = hex.EncodeToString(sum[:4])
	}
	return "hold-" + slug
}

// takeRepeated pulls every `--name value` / `--name=value` of the named options out of args (they may be given more than once, and a value
// may list several, comma separated) and returns the rest.
func takeRepeated(args []string, names ...string) (got map[string][]string, rest []string, err error) {
	got = map[string][]string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		taken := false
		for _, n := range names {
			flag := "--" + n
			switch {
			case a == flag:
				if i+1 >= len(args) {
					return nil, nil, fmt.Errorf("%s needs a value", flag)
				}
				i++
				got[n] = append(got[n], splitList(args[i])...)
				taken = true
			case strings.HasPrefix(a, flag+"="):
				got[n] = append(got[n], splitList(strings.TrimPrefix(a, flag+"="))...)
				taken = true
			}
		}
		if !taken {
			rest = append(rest, a)
		}
	}
	return got, rest, nil
}

func splitList(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func wwdHold(ctx context.Context, args []string, env Env) int {
	rep, rest, err := takeRepeated(args, "allow-class", "allow-repo")
	if err != nil {
		return usageErr(env, WWDUsage, err)
	}
	flags, pos, err := splitFlags(rest, "release")
	if err == nil {
		err = only(flags, "for", "reason", "expect", "release")
	}
	if err == nil && len(pos) != 1 {
		err = fmt.Errorf("hold takes one host")
	}
	if err != nil {
		return usageErr(env, WWDUsage, err)
	}
	host := pos[0]
	if !wire.ValidAlias(host) {
		return usageErr(env, WWDUsage, fmt.Errorf("%q is not a host alias", host))
	}
	d, err := time.ParseDuration(flags["for"])
	if flags["for"] == "" || err != nil || d <= 0 {
		return usageErr(env, WWDUsage, fmt.Errorf("--for must be a duration from now such as 30m or 2h"))
	}
	reason := strings.TrimSpace(flags["reason"])
	if reason == "" || utf8.RuneCountInString(reason) > maxReason {
		return usageErr(env, WWDUsage, fmt.Errorf("--reason is needed (1 to %d characters): what the host is kept quiet for", maxReason))
	}
	until := env.now().Add(d).UTC()
	e := wire.Exception{Name: holdName(reason), Until: &until, Holds: []wire.Hold{wire.HoldHeavy, wire.HoldLight}}
	classes, repos := rep["allow-class"], rep["allow-repo"]
	if len(classes) > 0 || len(repos) > 0 {
		a := wire.HoldAllow{Classes: []wire.JobClass{}, Repos: slices.Clone(repos)}
		if a.Repos == nil {
			a.Repos = []string{}
		}
		for _, c := range classes {
			jc := wire.JobClass(c)
			if jc != wire.ClassRelease && jc != wire.ClassMerge && jc != wire.ClassDev {
				return usageErr(env, WWDUsage, fmt.Errorf("--allow-class %q: release, merge or dev", c))
			}
			a.Classes = append(a.Classes, jc)
		}
		e.Allow = &a
	}
	if flags["release"] == "true" {
		e.Flags = append(e.Flags, wire.HoldFlagRelease)
	}
	if v := flags["expect"]; v != "" {
		if v != "pressure" {
			return usageErr(env, WWDUsage, fmt.Errorf("--expect takes pressure"))
		}
		e.Flags = append(e.Flags, wire.HoldFlagExpectPressure)
	}
	if err := e.Validate(); err != nil {
		return usageErr(env, WWDUsage, err)
	}

	if err := env.API.StartExceptionOn(ctx, host, e); err != nil {
		var se *client.StatusError
		if errors.As(err, &se) && se.Code == http.StatusBadRequest && strings.Contains(se.Body, "unknown field") && (e.Allow != nil || len(e.Flags) > 0) {
			fmt.Fprintf(env.Stderr, "pdx wwd hold: the wwd on %s does not know holds yet (it refused %s); it has to be upgraded first, or leave out the --allow-*, --release and --expect options\n", host, strings.TrimSpace(se.Body))
			return ExitFailed
		}
		return fail(env, "wwd hold", err)
	}
	if env.Log != nil {
		fmt.Fprintf(env.Log, "%s hold %s on %s until %s: %s\n", env.now().UTC().Format(time.RFC3339), e.Name, host, until.Format(time.RFC3339), reason)
	}

	polls := int(env.holdWait() / holdPoll)
	for i := 0; ; i++ {
		seen, err := holdShows(ctx, env.API, host, e)
		if err != nil {
			return fail(env, "wwd hold", err)
		}
		if seen {
			break
		}
		if i >= polls || ctx.Err() != nil {
			fmt.Fprintf(env.Stderr, "pdx wwd hold: %s is saved on %s but does not show in its facts after %s: it is not known to be in force (the worker decides from the facts); look at `pdx wwd pool` and ask again, or end it with `pdx wwd exception stop %s`\n", e.Name, host, env.holdWait(), e.Name)
			return ExitFailed
		}
		env.sleep(ctx, holdPoll)
	}
	fmt.Fprintf(env.Stdout, "hold %s in force on %s until %s  (%s)\n", e.Name, host, until.Format(time.RFC3339), reason)
	fmt.Fprintf(env.Stdout, "  admits %s\n", describeAllow(e.Allow))
	if len(e.Flags) > 0 {
		fmt.Fprintf(env.Stdout, "  flags %v\n", e.Flags)
	}
	fmt.Fprintf(env.Stdout, "  end it with: pdx wwd exception stop %s --host %s\n", e.Name, host)
	return ExitOK
}

func describeAllow(a *wire.HoldAllow) string {
	if a == nil || (len(a.Classes) == 0 && len(a.Repos) == 0) {
		return "nothing new (everything waits)"
	}
	var parts []string
	if len(a.Classes) > 0 {
		cs := make([]string, len(a.Classes))
		for i, c := range a.Classes {
			cs[i] = string(c)
		}
		parts = append(parts, "class "+strings.Join(cs, ", "))
	}
	if len(a.Repos) > 0 {
		parts = append(parts, "repo "+strings.Join(a.Repos, ", "))
	}
	return strings.Join(parts, "; ") + " only"
}

// holdShows says whether the host's facts carry this very hold: the name, the end, the allow and the flags. (An older hold of the same
// name is not it.)
func holdShows(ctx context.Context, api API, host string, e wire.Exception) (bool, error) {
	pool, err := api.Pool(ctx)
	if err != nil {
		return false, err
	}
	for _, p := range pool.Peers {
		if p.Info.Alias != host || p.Facts == nil {
			continue
		}
		for _, fe := range p.Facts.Exception {
			if fe.Name == e.Name && sameEnd(fe.Until, e.Until) && sameAllow(fe.Allow, e.Allow) && sameFlags(fe.Flags, e.Flags) {
				return true, nil
			}
		}
	}
	return false, nil
}

func sameEnd(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Truncate(time.Millisecond).Equal(b.Truncate(time.Millisecond)) // wwd.db keeps milliseconds
}

func sameAllow(a, b *wire.HoldAllow) bool {
	if a == nil || b == nil {
		return a == b
	}
	return slices.Equal(a.Classes, b.Classes) && slices.Equal(a.Repos, b.Repos)
}

func sameFlags(a, b []wire.HoldFlag) bool { return slices.Equal(a, b) }
