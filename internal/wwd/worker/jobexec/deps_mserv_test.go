//go:build mserv && linux

package jobexec

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/run"
	"github.com/wake/purdex/internal/wwd/snapshot"
	"github.com/wake/purdex/internal/wwd/wire"
	"github.com/wake/purdex/internal/wwd/worker/ident"
)

// #3620 on a real Linux host (mserv): the dependency install runs in the job's sandbox (a transient wwd.slice service) with only the
// workspace and deps_root writable. Build it on another host and copy the binary:
//
//	GOOS=linux GOARCH=amd64 go test -c -tags mserv -o wwd-jobexec-mserv.test ./internal/wwd/worker/jobexec/
//	WWD_MSERV_E2E=1 ./wwd-jobexec-mserv.test -test.v -test.run MServDeps
//
// It uses a pnpm and a node that are shell scripts (nothing is downloaded, no network), a directory under WWD_E2E_DIR (default
// ~/.local/share/wwd-deps-e2e) and transient units named wwd-<origin tag>-j_depse2e-<n>; it resets only those.
func TestMServDepsInstallRunsInTheSandboxWithOnlyTheCacheWritable(t *testing.T) {
	if os.Getenv("WWD_MSERV_E2E") != "1" {
		t.Skip("set WWD_MSERV_E2E=1 on mserv")
	}
	base := os.Getenv("WWD_E2E_DIR")
	if base == "" {
		base = filepath.Join(os.Getenv("HOME"), ".local", "share", "wwd-deps-e2e")
	}
	require.NoError(t, os.RemoveAll(base))
	require.NoError(t, os.MkdirAll(base, 0o755))
	t.Cleanup(func() { _ = os.RemoveAll(base) })

	bin, depsDir := filepath.Join(base, "bin"), filepath.Join(base, "deps")
	outside := filepath.Join(base, "outside-the-workspace-and-the-cache")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	script := func(name, body string) {
		require.NoError(t, os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body), 0o755))
	}
	script("node", `echo v24.0.0`)
	script("pnpm", `case "$1" in
  --version) echo 9.0.0;;
  install)
    echo ran >> "$npm_config_store_dir/ran.log"
    if ( echo x > `+outside+` ) 2>/dev/null; then echo written > node_modules.outside; else echo denied > .outside-result; fi
    if ( echo x > "$npm_config_store_dir/../entries/poisoned" ) 2>/dev/null; then echo written > .entries-result; else echo denied > .entries-result; fi
    if ( echo x > "$npm_config_store_dir/../pnpm-store.lock" ) 2>/dev/null; then echo written > .lock-result; else echo denied > .lock-result; fi
    mkdir -p node_modules/pkg && echo installed > node_modules/pkg/index.js
    mkdir -p spa/node_modules/dep && echo workspace > spa/node_modules/dep/index.js;;
esac
`)

	sb := run.Sandbox{User: os.Getenv("USER")}
	e := New(Config{
		WorkRoot: filepath.Join(base, "work"), Sandbox: &sb, ParentEnv: []string{"PATH=" + bin + ":/usr/bin:/bin"}, DepsDir: depsDir, DepsKeep: 2,
		KillAfter: 3 * time.Second,
	})
	lock := "lockfileVersion: '9.0'\n\nimporters:\n\n  .:\n    dependencies:\n      a:\n        version: 1\n\n  spa:\n    devDependencies:\n      b:\n        version: 1\n"
	prepare := func(n string) string {
		ws := filepath.Join(base, "work", n, "ws")
		require.NoError(t, os.MkdirAll(filepath.Join(ws, "spa"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(ws, "pnpm-lock.yaml"), []byte(lock), 0o644))
		id := "j_depse2e-" + n
		ref := ident.Ref{Source: "mserv", ID: id}
		t.Cleanup(func() {
			_ = exec.Command("sudo", "systemctl", "reset-failed", run.UnitName(ref.Tag(), id)+".service").Run() // our own unit only
		})
		j := wire.Job{ID: id, Origin: "mserv", Class: wire.ClassDev, Repo: wire.Repo{Name: "purdex"}}
		require.NoError(t, e.prepareDeps(context.Background(), ref, j, snapshot.Workspace{Dir: ws}, []string{"pnpm-lock.yaml"}, nil))
		return ws
	}

	ws1 := prepare("1")
	b, err := os.ReadFile(filepath.Join(ws1, "node_modules", "pkg", "index.js"))
	require.NoError(t, err, "the install made node_modules in the sandbox")
	assert.Equal(t, "installed\n", string(b))
	res, err := os.ReadFile(filepath.Join(ws1, ".outside-result"))
	require.NoError(t, err)
	assert.Equal(t, "denied\n", string(res), "the install could not write outside the workspace and the cache")
	for _, name := range []string{".entries-result", ".lock-result"} {
		r, err := os.ReadFile(filepath.Join(ws1, name))
		require.NoError(t, err)
		assert.Equal(t, "denied\n", string(r), "the install could not write the installed sets or the locks in deps_root (%s)", name)
	}
	_, err = os.Stat(outside)
	assert.True(t, os.IsNotExist(err), "nothing was written outside")
	ran, err := os.ReadFile(filepath.Join(depsDir, "pnpm-store", "ran.log"))
	require.NoError(t, err, "the download cache in deps_root was writable for the install")
	assert.Equal(t, 1, strings.Count(string(ran), "ran"))

	ws2 := prepare("2")
	_, err = os.Stat(filepath.Join(ws2, "spa", "node_modules", "dep", "index.js"))
	require.NoError(t, err, "the second workspace had its dependencies cloned, the member's too")
	ran, _ = os.ReadFile(filepath.Join(depsDir, "pnpm-store", "ran.log"))
	assert.Equal(t, 1, strings.Count(string(ran), "ran"), "and nothing was installed again")

	out, _ := exec.Command("systemctl", "list-units", "--all", "--no-legend", "wwd-*").Output()
	for _, l := range strings.Split(string(out), "\n") {
		assert.NotContains(t, l, "j_depse2e", "a unit of this test is left: %s", l)
	}
}
