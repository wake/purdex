package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/entry"
	"github.com/wake/purdex/internal/wwd/wire"
)

// a repo whose queued task needs a label, and a local one that needs nothing
func entryPoolRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".wwd"), 0o755))
	src := "[entry]\nmode = \"rewrite\"\n" +
		"[tasks.vitest-root]\nrun = \"pnpm vitest run\"\nargs = \"none\"\nlabels = [\"repo:aigora-v2\"]\n" +
		"[tasks.lint]\nrun = \"make lint\"\nargs = \"none\"\nlocal = true\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, ".wwd", "tasks.toml"), []byte(src), 0o644))
	return root
}

// withPool stands in for the local wwd's pool and counts the reads.
func withPool(t *testing.T, pool wire.Pool, err error) *int {
	t.Helper()
	old := entryPool
	n := 0
	entryPool = func(context.Context) (wire.Pool, error) { n++; return pool, err }
	t.Cleanup(func() { entryPool = old })
	return &n
}

func entryAnswer(t *testing.T, root string, argv ...string) entry.Result {
	t.Helper()
	code, out, errs := callEntry(t, append([]string{"match", "--json", "--cwd", root, "--"}, argv...)...)
	require.Equal(t, 0, code, errs)
	var r entry.Result
	require.NoError(t, json.Unmarshal([]byte(out), &r))
	return r
}

func peerOf(alias string, reachable, workerOn bool, labels ...string) wire.Peer {
	return wire.Peer{Info: wire.Info{HostID: alias, Alias: alias, Labels: labels, Worker: wire.InfoWorker{Enabled: workerOn}}, Reachable: reachable, Facts: &wire.Facts{}}
}

// The aigora case: the task needs repo:aigora-v2 and no host has it, so the command is not rewritten into something that would end with exit 5.
func TestEntryMatch_NoHostCanTakeAQueuedTaskIsNone(t *testing.T) {
	root := entryPoolRepo(t)
	withPool(t, wire.Pool{Peers: []wire.Peer{peerOf("mlab", true, true, "os:darwin"), peerOf("air26", true, true, "repo:purdex")}}, nil)
	r := entryAnswer(t, root, "pnpm", "vitest", "run")
	assert.Equal(t, entry.ActionNone, r.Action)
	assert.Contains(t, r.Reason, "no reachable host with a worker on holds repo:aigora-v2")
}

func TestEntryMatch_AHostThatCanTakeItKeepsTheRewrite(t *testing.T) {
	root := entryPoolRepo(t)
	withPool(t, wire.Pool{Peers: []wire.Peer{peerOf("mlab", true, true, "repo:aigora-v2")}}, nil)
	r := entryAnswer(t, root, "pnpm", "vitest", "run")
	assert.Equal(t, entry.ActionRewrite, r.Action)
	assert.Equal(t, "vitest-root", r.Task)
	assert.True(t, r.Queued)
}

func TestEntryMatch_AHostThatHoldsTheLabelsButIsDownIsNone(t *testing.T) {
	root := entryPoolRepo(t)
	withPool(t, wire.Pool{Peers: []wire.Peer{peerOf("mlab", false, true, "repo:aigora-v2")}}, nil)
	assert.Equal(t, entry.ActionNone, entryAnswer(t, root, "pnpm", "vitest", "run").Action)
}

func TestEntryMatch_APoolThatCannotBeReadIsNone(t *testing.T) {
	root := entryPoolRepo(t)
	withPool(t, wire.Pool{}, errors.New("connect: no such file or directory"))
	r := entryAnswer(t, root, "pnpm", "vitest", "run")
	assert.Equal(t, entry.ActionNone, r.Action)
	assert.Contains(t, r.Reason, "cannot be read")
}

// The shape of the pool right after wwd starts: this host answers (reachable, worker on) but its first facts record has not come, so only its
// os and arch labels are announced. A queued task is not turned into none for that: it would be queued as usual.
func TestEntryMatch_AHostStillSettlingKeepsTheRewrite(t *testing.T) {
	root := entryPoolRepo(t)
	settling := peerOf("mlab", true, true, "os:darwin", "arch:arm64")
	settling.Facts = nil
	withPool(t, wire.Pool{Peers: []wire.Peer{settling}}, nil)
	r := entryAnswer(t, root, "pnpm", "vitest", "run")
	assert.Equal(t, entry.ActionRewrite, r.Action)
	assert.Equal(t, "vitest-root", r.Task)
}

// A local task needs no other host: the pool is not even read.
func TestEntryMatch_ALocalTaskDoesNotReadThePool(t *testing.T) {
	root := entryPoolRepo(t)
	reads := withPool(t, wire.Pool{}, errors.New("must not be asked"))
	r := entryAnswer(t, root, "make", "lint")
	assert.Equal(t, entry.ActionRewrite, r.Action)
	assert.Equal(t, "lint", r.Task)
	assert.Zero(t, *reads)
}
