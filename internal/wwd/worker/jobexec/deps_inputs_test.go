package jobexec

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/run"
	"github.com/wake/purdex/internal/wwd/snapshot"
	"github.com/wake/purdex/internal/wwd/wire"
	"github.com/wake/purdex/internal/wwd/worker/ident"
)

// manualWS is a workspace made by hand (a job directory with ws in it) holding the given files.
func manualWS(t *testing.T, files map[string]string) snapshot.Workspace {
	t.Helper()
	ws := filepath.Join(t.TempDir(), "job", "ws")
	for rel, body := range files {
		p := filepath.Join(ws, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}
	require.NoError(t, os.MkdirAll(ws, 0o755))
	return snapshot.Workspace{Dir: ws}
}

func prepareManual(t *testing.T, e *Exec, ws snapshot.Workspace, taskEnv []string) error {
	t.Helper()
	j := job("withdeps")
	return e.prepareDeps(context.Background(), ident.Ref{Source: "mlab", ID: j.ID}, j, ws, []string{"pnpm-lock.yaml"}, taskEnv)
}

// Two workspaces with one lockfile are one entry only if everything else an install reads is the same: the root package's manifest (its
// lifecycle scripts), the package manager's configuration, a member's manifest and the environment the install is told. What differs
// makes another entry; what does not, shares one.
func TestWhatAnInstallReadsBesidesTheLockfileIsPartOfTheKey(t *testing.T) {
	base := map[string]string{"pnpm-lock.yaml": depsLock, "package.json": `{"scripts":{"postinstall":"a"}}`, "spa/package.json": `{"name":"spa"}`}
	with := func(over map[string]string) map[string]string {
		out := map[string]string{}
		for k, v := range base {
			out[k] = v
		}
		for k, v := range over {
			out[k] = v
		}
		return out
	}
	for name, tc := range map[string]struct {
		files map[string]string
		env   []string
		same  bool
	}{
		"nothing differs":                {files: with(nil), same: true},
		"the root's postinstall":         {files: with(map[string]string{"package.json": `{"scripts":{"postinstall":"b"}}`})},
		"an .npmrc":                      {files: with(map[string]string{".npmrc": "registry=https://evil.example\n"})},
		"a member's manifest":            {files: with(map[string]string{"spa/package.json": `{"name":"spa","scripts":{"postinstall":"c"}}`})},
		"a workspace definition":         {files: with(map[string]string{"pnpm-workspace.yaml": "packages:\n  - spa\n"})},
		"a .pnpmfile.cjs":                {files: with(map[string]string{".pnpmfile.cjs": "module.exports = { hooks: {} }\n"})},
		"the environment of the install": {files: with(nil), env: []string{"HTTPS_PROXY=http://proxy:3128"}},
	} {
		r := newDepsRig(t)
		first := manualWS(t, base)
		require.NoError(t, prepareManual(t, r.e, first, nil), name)
		require.Equal(t, 1, r.tools.installs(t), name)
		second := manualWS(t, tc.files)
		require.NoError(t, prepareManual(t, r.e, second, tc.env), name)
		if tc.same {
			assert.Equal(t, 1, r.tools.installs(t), "%s: shares the entry", name)
		} else {
			assert.Equal(t, 2, r.tools.installs(t), "%s: is another entry", name)
		}
	}
}

// The install may write its own package manager's download cache and nothing else of the cache directory: not the installed sets, not
// their locks.
func TestTheInstallsSandboxIsOnlyItsOwnDownloadCache(t *testing.T) {
	r := newDepsRig(t)
	r.e.cfg.Sandbox = &run.Sandbox{User: "wake", ReadWritePaths: []string{"/srv/a"}}
	j := job("withdeps")
	ws := snapshot.Workspace{Dir: filepath.Join(t.TempDir(), "ws")}
	in := installers["pnpm-lock.yaml"]
	cacheDir := filepath.Join(r.dir, in.cacheSub)
	spec := r.e.installSpec(ident.Ref{Source: "mlab", ID: j.ID}, j, ws, "pnpm-lock.yaml", in, nil, cacheDir)
	require.NotNil(t, spec.Sandbox)
	assert.Equal(t, []string{"/srv/a", cacheDir}, spec.Sandbox.ReadWritePaths)
	assert.NotContains(t, spec.Sandbox.ReadWritePaths, r.dir, "the cache directory as a whole is not writable for an install")
	assert.Equal(t, []string{"/srv/a"}, r.e.cfg.Sandbox.ReadWritePaths)
	var _ wire.JobClass
}

// A download cache over its bound is emptied before the next install, once nobody else is using it; one under the bound is left, and one
// that another install is using is left however big it is.
func TestADownloadCacheOverItsBoundIsEmptiedBeforeAnInstall(t *testing.T) {
	fill := func(t *testing.T, dir string, mb int) {
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "blob"), make([]byte, mb<<20), 0o644))
	}
	ran := false
	install := func() error { ran = true; return nil }

	over := filepath.Join(t.TempDir(), "pnpm-store")
	fill(t, over, 2)
	require.NoError(t, withStore(context.Background(), over, 1<<20, install))
	assert.True(t, ran)
	_, err := os.Stat(filepath.Join(over, "blob"))
	assert.True(t, os.IsNotExist(err), "emptied")
	st, err := os.Stat(over)
	require.NoError(t, err)
	assert.True(t, st.IsDir(), "and made again for the install")

	under := filepath.Join(t.TempDir(), "pnpm-store")
	fill(t, under, 1)
	require.NoError(t, withStore(context.Background(), under, 2<<20, install))
	_, err = os.Stat(filepath.Join(under, "blob"))
	assert.NoError(t, err, "under the bound it stays")

	busy := filepath.Join(t.TempDir(), "pnpm-store")
	fill(t, busy, 2)
	other, err := os.OpenFile(busy+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	require.NoError(t, err)
	defer other.Close()
	require.NoError(t, syscall.Flock(int(other.Fd()), syscall.LOCK_SH)) // another install is using it
	require.NoError(t, withStore(context.Background(), busy, 1<<20, install))
	_, err = os.Stat(filepath.Join(busy, "blob"))
	assert.NoError(t, err, "not emptied under an install that is using it")
}

// A download cache of many small files is over its bound though it is small in bytes, and the look at it stops at the entry bound instead of
// walking them all; a look that is cancelled ends and the install does not run.
func TestTheLookAtADownloadCacheIsBounded(t *testing.T) {
	old := storeScanLimit
	storeScanLimit = 50
	t.Cleanup(func() { storeScanLimit = old })
	dir := filepath.Join(t.TempDir(), "npm-cache")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	for i := 0; i < 80; i++ {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "f"+string(rune('a'+i%26))+string(rune('a'+i/26))), nil, 0o644))
	}
	ran := false
	require.NoError(t, withStore(context.Background(), dir, 1<<40, func() error { ran = true; return nil }))
	assert.True(t, ran)
	list, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, list, "empty files, far under the bound in bytes, but more entries than the scan allows: emptied")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ran = false
	require.NoError(t, os.WriteFile(filepath.Join(dir, "x"), nil, 0o644))
	err = withStore(ctx, dir, 1<<40, func() error { ran = true; return nil })
	require.Error(t, err)
	assert.False(t, ran, "a cancelled look does not go on to the install")
}

// The bound is worker.deps_store_max_mb, 4096 when it is not set.
func TestTheDownloadCacheBoundComesFromTheConfig(t *testing.T) {
	assert.EqualValues(t, 4096<<20, (&Exec{}).storeMax())
	assert.EqualValues(t, 512<<20, (&Exec{cfg: Config{DepsStoreMaxMB: 512}}).storeMax())
}

// A real install goes through the guard: a cache over the bound is emptied and the install runs and fills it again.
func TestAnInstallThatNeedsToRunEmptiesAnOversizedDownloadCache(t *testing.T) {
	r := newDepsRig(t)
	store := filepath.Join(r.dir, "pnpm-store")
	require.NoError(t, os.MkdirAll(store, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(store, "old-blob"), make([]byte, 2<<20), 0o644))
	r.e.cfg.DepsStoreMaxMB = 1
	_, err := r.prepare(t, r.e, "j_one")
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(store, "old-blob"))
	assert.True(t, os.IsNotExist(err), "the oversized download cache was emptied before the install")
	assert.Equal(t, 1, r.tools.installs(t))
}
