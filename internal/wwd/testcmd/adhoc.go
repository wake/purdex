package testcmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/wake/purdex/internal/gate"
	"github.com/wake/purdex/internal/wwd/client"
	"github.com/wake/purdex/internal/wwd/fastlane"
	"github.com/wake/purdex/internal/wwd/manifest"
	"github.com/wake/purdex/internal/wwd/submit"
	"github.com/wake/purdex/internal/wwd/wire"
)

// adHoc is `pdx test --adhoc -- <command…>` (spec §8.3): a command that is not a declared task. It goes the way of the fast lane —
// here, in the caller's working tree, the words as the argv with no shell — under a heavy slot of the gate, recorded as
// undeclared. It is never queued, so it never reaches another host. A wwd that is not running only means no record.
func adHoc(ctx context.Context, o Options) int {
	if o.Gate == nil {
		fmt.Fprintln(o.Stderr, "pdx test: there is no gate to run the command under")
		return ExitFailed
	}
	sub := wire.Submitter{Session: o.Session, Ref: o.Ref, Team: o.Team}
	switch info, err := o.API.Info(ctx); {
	case err == nil:
		sub.Host = info.Alias
	case errors.Is(err, client.ErrUnavailable):
		o.Recorder = nil
	default:
		return failWWD(o, err)
	}
	root, err := submit.RepoRoot(ctx, o.Dir, o.GitBin)
	if err != nil {
		return failPrepare(o, err)
	}
	argv := o.Args.Argv()
	var body submit.Body
	treeNote := ""
	if o.Recorder != nil {
		p, err := submit.Prepare(ctx, submit.Request{Dir: o.Dir, AdHoc: argv, Submitter: sub, Git: o.GitBin})
		switch {
		case errors.Is(err, submit.ErrNoCommit): // nothing to record against: the command goes on unrecorded
			o.Recorder = nil
		case err != nil:
			return failPrepare(o, err)
		default:
			body, treeNote = p.Body, p.Note
		}
	}
	task := manifest.AdHocTask()
	if task.Cwd, err = submit.RelDir(root, o.Dir); err != nil { // where the caller stands, not the top of the repository
		return failPrepare(o, err)
	}
	req := fastlane.Request{Root: root, Task: task, Args: argv, Body: body, TreeNote: treeNote, Gate: o.Gate, GateClass: gate.ClassHeavy, Recorder: o.Recorder, Env: o.Env}
	code, _ := runFast(ctx, o, task, req, "adhoc", "undeclared command: it ran here only, under a heavy slot", true)
	return code
}
