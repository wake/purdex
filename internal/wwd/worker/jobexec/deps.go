package jobexec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wake/purdex/internal/execstat"
	"github.com/wake/purdex/internal/wwd/deps"
	"github.com/wake/purdex/internal/wwd/run"
	"github.com/wake/purdex/internal/wwd/snapshot"
	"github.com/wake/purdex/internal/wwd/wire"
	"github.com/wake/purdex/internal/wwd/worker/ident"
	"github.com/wake/purdex/internal/wwd/worker/qos"
	"github.com/wake/purdex/internal/wwd/worker/runner"
)

// The dependency cache in a job (spec §5 "Workspace"): a task that declares `deps` (lockfiles) gets its installed dependencies cloned from
// a cache keyed by the lockfiles' contents; a key with no entry is installed once, in the job's own workspace and under the job's own
// limits (the sandbox, QoS, the clean environment), and then filed. The package managers' download caches live in the cache directory
// too, so an install that has to happen starts from packages this host has already fetched.

const (
	// depsInstallTimeout bounds one install (several lockfiles share it).
	depsInstallTimeout = 15 * time.Minute
	// depsFailLimit is how many times in a row the install of one key fails on this host before a job of it is ended instead of being
	// given back to the queue to be installed again; depsFailWindow is how long a failure is remembered (a registry that is down comes back).
	depsFailLimit  = 2
	depsFailWindow = 10 * time.Minute
)

// installer describes how a lockfile is installed: the command, the cache the package manager keeps (an environment variable that points it at
// the cache directory, under the name of the subdirectory), and the tools whose versions are part of the key.
type installer struct {
	argv     []string
	cacheEnv string // the variable that names the package manager's download cache
	cacheSub string // its subdirectory of the cache directory
	tools    []string
	// asWorker: the install is the worker's own, outside the job's sandbox and with the worker's own HOME and environment, because it
	// has to reach a private host the job has no credentials for (Go modules from the company Gitea). It fills the dependency
	// directory in the workspace, which is what the job then uses, offline.
	asWorker bool
}

// goPrivate is the host of the private Go modules (lab.protype.tw/wake/nexen): the download names it itself rather than leave it to the go
// env of the host it runs on.
const goPrivate = "lab.protype.tw"

var installers = map[string]installer{
	"pnpm-lock.yaml":    {argv: []string{"pnpm", "install", "--frozen-lockfile"}, cacheEnv: "npm_config_store_dir", cacheSub: "pnpm-store", tools: []string{"pnpm", "node"}},
	"package-lock.json": {argv: []string{"npm", "ci"}, cacheEnv: "npm_config_cache", cacheSub: "npm-cache", tools: []string{"npm", "node"}},
	"yarn.lock":         {argv: []string{"yarn", "install", "--frozen-lockfile"}, cacheEnv: "YARN_CACHE_FOLDER", cacheSub: "yarn-cache", tools: []string{"yarn", "node"}},
	"composer.lock":     {argv: []string{"composer", "install", "--no-interaction", "--no-progress"}, cacheEnv: "COMPOSER_CACHE_DIR", cacheSub: "composer-cache", tools: []string{"composer", "php"}},
	"go.sum":            {argv: []string{"go", "mod", "download"}, tools: []string{"go"}, asWorker: true},
}

// depsState is what an Exec keeps for the dependency cache.
type depsState struct {
	once  sync.Once
	cache *deps.Cache
	err   error

	now      func() time.Time // the clock failures are dated by (a test sets it); nil: time.Now
	mu       sync.Mutex
	versions map[string]string // tool → its --version, for the life of the process (a change of tools takes effect when wwd restarts)
	fails    map[string]depsFail
}

type depsFail struct {
	n     int
	since time.Time // the first of the failures in a row
}

func (e *Exec) depsCache() (*deps.Cache, error) {
	e.deps.once.Do(func() {
		if e.cfg.DepsDir == "" {
			e.deps.err = errors.New("jobexec: this worker has no dependency cache directory")
			return
		}
		keep := e.cfg.DepsKeep
		if keep <= 0 {
			keep = 2
		}
		e.deps.cache, e.deps.err = deps.NewCache(filepath.Join(e.cfg.DepsDir, "entries"), keep)
	})
	return e.deps.cache, e.deps.err
}

// prepareDeps puts the installed dependencies of the task's lockfiles in the job's workspace. A failed install is the worker's own trouble
// (the job goes back to the queue), until the same key has failed depsFailLimit times in a row on this host within depsFailWindow: then
// the job ends, as failed with no exit and a reason, which no outcome and no qualification counts, and the host stops trying that key
// until the window has passed.
func (e *Exec) prepareDeps(ctx context.Context, ref ident.Ref, job wire.Job, ws snapshot.Workspace, lockfiles []string, taskEnv []string) error {
	goSums := 0
	for _, lf := range lockfiles {
		if path.Base(lf) == "go.sum" {
			goSums++
		}
	}
	if goSums > 1 { // the job is given one module cache (GOMODCACHE), so it can use the modules of one go.sum
		return &runner.Rejected{Reason: "deps_one_go_sum: a task may declare one go.sum"}
	}
	cache, err := e.depsCache()
	if err != nil {
		return err
	}
	return e.prepareDepsWith(ctx, cache, ref, job, ws, lockfiles, taskEnv)
}

// depsJobEnv is what the job's command is told about the dependencies it was given besides the directories: a Go task uses the module cache
// in its workspace and nothing else (no proxy, no network, no change to go.mod or go.sum), so it needs neither credentials nor a route out.
// These come last in the task's environment, so nothing the manifest lists can point Go elsewhere.
// The task's own GOFLAGS stay (build tags, -p), with -mod=readonly after them: the later -mod is the one that counts. goCache, when the job
// was given the shared build cache (gocache.go), is its GOCACHE, and -count=1 comes with it: the test results in a cache other jobs write to
// are not used (a command that names -count itself keeps its own: the command line wins over GOFLAGS).
func depsJobEnv(ws string, lockfiles []string, taskEnv []string, goCache string) []string {
	for _, lf := range lockfiles {
		if path.Base(lf) == "go.sum" {
			flags := "-mod=readonly"
			for _, kv := range taskEnv {
				if v, ok := strings.CutPrefix(kv, "GOFLAGS="); ok && strings.TrimSpace(v) != "" {
					flags = strings.TrimSpace(v) + " -mod=readonly"
				}
			}
			env := []string{"GOMODCACHE=" + filepath.Join(ws, filepath.FromSlash(path.Dir(lf)), ".gomodcache"), "GOPROXY=off", "GOTOOLCHAIN=local", "GOWORK=off"}
			if goCache != "" {
				flags += " -count=1"
				env = append(env, "GOCACHE="+goCache)
			}
			return append(env, "GOFLAGS="+flags)
		}
	}
	return nil
}

func (e *Exec) prepareDepsWith(ctx context.Context, cache *deps.Cache, ref ident.Ref, job wire.Job, ws snapshot.Workspace, lockfiles []string, taskEnv []string) error {
	var in []installer
	for _, lf := range lockfiles {
		i, ok := installers[path.Base(lf)]
		if !ok {
			return &runner.Rejected{Reason: fmt.Sprintf("deps_unknown_lockfile: %q is not a lockfile this worker installs", lf)}
		}
		in = append(in, i)
	}
	fingerprint, err := e.depsFingerprint(ctx, in, taskEnv)
	if err != nil {
		return err
	}
	dirs, err := workspaceDirs(ws.Dir, lockfiles)
	if err != nil {
		return err
	}
	spec := deps.Spec{Lockfiles: lockfiles, Dirs: dirs, Inputs: depsInputs(lockfiles, dirs), Fingerprint: fingerprint}
	key, err := cache.Key(ws.Dir, spec)
	if err != nil {
		return err
	}
	if reason := e.depsHeldOff(key); reason != "" {
		return &runner.Rejected{Reason: reason}
	}
	spec.Install = func(ctx context.Context, dir string) error {
		return e.installDeps(ctx, ref, job, ws, lockfiles, in, taskEnv)
	}
	_, err = cache.Prepare(ctx, ws.Dir, spec)
	if err == nil {
		e.depsOK(key)
		return nil
	}
	if ctx.Err() != nil { // cancelled or shutting down: not a failure of the install
		return ctx.Err()
	}
	if n := e.depsFailed(key); n >= depsFailLimit {
		return &runner.Rejected{Reason: fmt.Sprintf("deps_install_failed: the dependencies (key %s) failed to install %d times in a row on this host: %v", key, n, err)}
	}
	return err
}

// installDeps runs the install of each lockfile in its own directory, as a job's command is run (run.Run: the sandbox, the clean
// environment with the package managers' download caches in the cache directory). Its log is <out>/deps-install, apart from the job's.
func (e *Exec) installDeps(ctx context.Context, ref ident.Ref, job wire.Job, ws snapshot.Workspace, lockfiles []string, in []installer, taskEnv []string) error {
	for i, lf := range lockfiles {
		if in[i].asWorker {
			if err := e.downloadModules(ctx, job, ws, lf, in[i], taskEnv); err != nil {
				return err
			}
			continue
		}
		cacheDir := filepath.Join(e.cfg.DepsDir, in[i].cacheSub)
		if err := os.MkdirAll(cacheDir, 0o755); err != nil {
			return err
		}
		err := withStore(ctx, cacheDir, e.storeMax(), func() error {
			res := run.Run(ctx, e.installSpec(ref, job, ws, lf, in[i], taskEnv, cacheDir))
			switch {
			case res.Outcome == run.Exited && res.ExitCode == 0 && res.Err == nil:
				return nil
			case res.Err != nil:
				return fmt.Errorf("install %s: %w", lf, res.Err)
			default:
				return fmt.Errorf("install %s: %s (exit %d)%s", lf, res.Outcome, res.ExitCode, installTail(res.Tail))
			}
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// installSpec is the run of one lockfile's install, under the job's own policy: the background QoS of a dev job where the host asks for it
// (the same wrapper as the job's command), the sandbox, the task's environment (the allow list and fixed values of the manifest, which
// a proxy or a registry setting comes through) and, last so that nothing in the task's list can point the package manager's
// download cache elsewhere, the cache directory.
func (e *Exec) installSpec(ref ident.Ref, job wire.Job, ws snapshot.Workspace, lockfile string, in installer, taskEnv []string, cacheDir string) run.Spec {
	base := filepath.Dir(ws.Dir)
	argv, _ := qos.For(job.Class, e.cfg.BackgroundDev, in.argv)
	return run.Spec{
		JobID: job.ID, Origin: ref.Tag(), Argv: argv, Dir: filepath.Join(ws.Dir, filepath.FromSlash(path.Dir(lockfile))), Workspace: base,
		TaskEnv: append(append([]string(nil), taskEnv...), in.cacheEnv+"="+cacheDir), ParentEnv: e.commandParentEnv(), Timeout: depsInstallTimeout,
		KillAfter: e.cfg.KillAfter, OutDir: filepath.Join(outDir(base), "deps-install"), Procs: e.cfg.Procs, Sandbox: depsSandbox(e.cfg.Sandbox, cacheDir),
	}
}

// depsSandbox is the sandbox of an install: the job's own, and the package manager's own download cache (outside the workspace, which is all a
// sandboxed command may write) let through. Not the cache directory as a whole: the installed sets and their locks are in it, and an
// install script must not be able to change what other jobs are given. The sandbox of the job itself is not touched.
func depsSandbox(sb *run.Sandbox, cacheDir string) *run.Sandbox {
	if sb == nil {
		return nil
	}
	c := *sb
	c.ReadWritePaths = append(append([]string(nil), c.ReadWritePaths...), cacheDir)
	return &c
}

func installTail(tail []byte) string {
	s := strings.TrimSpace(string(tail))
	if s == "" {
		return ""
	}
	if len(s) > 400 {
		s = s[len(s)-400:]
	}
	return ": " + s
}

// depsFingerprint is what the install depends on besides the lockfiles: the commands and the versions of the tools that run them.
func (e *Exec) depsFingerprint(ctx context.Context, in []installer, taskEnv []string) (string, error) {
	var parts []string
	envs := append([]string(nil), taskEnv...) // what the install is told besides: a proxy or a registry setting changes what it makes
	sort.Strings(envs)
	for _, kv := range envs {
		parts = append(parts, "env "+kv)
	}
	seen := map[string]bool{}
	for _, i := range in {
		parts = append(parts, strings.Join(i.argv, " "))
		for _, t := range i.tools {
			if seen[t] {
				continue
			}
			seen[t] = true
			v, err := e.toolVersion(ctx, t)
			if err != nil {
				return "", err
			}
			parts = append(parts, t+" "+v)
		}
	}
	return strings.Join(parts, "\n"), nil
}

// toolVersion is `<tool> --version` as a job would find the tool (the worker's job PATH), asked once per process.
func (e *Exec) toolVersion(ctx context.Context, tool string) (string, error) {
	e.deps.mu.Lock()
	if v, ok := e.deps.versions[tool]; ok {
		e.deps.mu.Unlock()
		return v, nil
	}
	e.deps.mu.Unlock()
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	prog, err := run.LookPath(tool, "", e.cfg.ParentEnv)
	if err != nil {
		return "", fmt.Errorf("jobexec: %s is not on the job PATH: %w", tool, err)
	}
	args := []string{"--version"}
	if tool == "go" { // go has no --version
		args = []string{"version"}
	}
	cmd := execstat.CommandContext(cctx, "wwd.jobexec.deps", prog, args...)
	cmd.Env = e.cfg.ParentEnv
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("jobexec: %s --version: %w", tool, err)
	}
	v := strings.TrimSpace(string(out))
	e.deps.mu.Lock()
	if e.deps.versions == nil {
		e.deps.versions = map[string]string{}
	}
	e.deps.versions[tool] = v
	e.deps.mu.Unlock()
	return v, nil
}

// depsHeldOff says why a job of this key is not tried: it failed depsFailLimit times in a row within depsFailWindow.
func (e *Exec) depsHeldOff(key string) string {
	e.deps.mu.Lock()
	defer e.deps.mu.Unlock()
	f, ok := e.deps.fails[key]
	if !ok {
		return ""
	}
	if e.depsNow().Sub(f.since) > depsFailWindow {
		delete(e.deps.fails, key)
		return ""
	}
	if f.n >= depsFailLimit {
		return fmt.Sprintf("deps_install_failed: the dependencies (key %s) failed to install %d times in a row on this host; not tried again before %s", key, f.n, f.since.Add(depsFailWindow).Format(time.TimeOnly))
	}
	return ""
}

// depsFailed counts a failure of the key and returns how many are in a row.
func (e *Exec) depsFailed(key string) int {
	e.deps.mu.Lock()
	defer e.deps.mu.Unlock()
	if e.deps.fails == nil {
		e.deps.fails = map[string]depsFail{}
	}
	f := e.deps.fails[key]
	if f.n == 0 || e.depsNow().Sub(f.since) > depsFailWindow {
		f = depsFail{since: e.depsNow()}
	}
	f.n++
	e.deps.fails[key] = f
	return f.n
}

func (e *Exec) depsNow() time.Time {
	if e.deps.now != nil {
		return e.deps.now()
	}
	return time.Now()
}

// depsInputs are the files, besides the lockfiles, that an install reads: next to each lockfile the package manifests, the package manager's
// configuration and a workspace's definition, and the manifest of each workspace member (dirs are the members' node_modules). A file that
// is not there is hashed as absent. The root package's own scripts and .npmrc are not functions of the lockfile, so without them two
// snapshots with one lockfile would share an installed tree that one of them made differently.
// This is a list, not a derivation: a file those name (a yarnPath or a plugin, a script of composer.json that runs a file of the workspace)
// is not followed. The lockfile already holds the hash of a pnpm patch, which is why patches are not listed.
func depsInputs(lockfiles, memberDirs []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(dir, name string) {
		p := path.Join(dir, name)
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, lf := range lockfiles {
		d := path.Dir(lf)
		for _, n := range []string{"package.json", ".npmrc", ".pnpmfile.cjs", ".yarnrc", ".yarnrc.yml", "pnpm-workspace.yaml", "composer.json", "go.mod"} {
			add(d, n)
		}
	}
	for _, m := range memberDirs {
		add(path.Dir(m), "package.json")
	}
	return out
}

// workspaceDirs are the node_modules of the members of a pnpm workspace: one lockfile at the root installs dependencies under every
// member that has any (purdex: spa and electron), and a task runs in a member's directory, so a set without them would clone a root
// node_modules and still not find vitest. They are read from the lockfile's own importers, so they are what the key's contents say.
func workspaceDirs(ws string, lockfiles []string) ([]string, error) {
	var out []string
	for _, lf := range lockfiles {
		if path.Base(lf) != "pnpm-lock.yaml" || !filepath.IsLocal(lf) {
			continue
		}
		root, err := os.OpenRoot(ws)
		if err != nil {
			return nil, err
		}
		b, err := root.ReadFile(lf) // through the root: a lockfile that is a link out of the workspace is not read
		root.Close()
		if err != nil {
			return nil, fmt.Errorf("jobexec: %w", err)
		}
		for _, m := range pnpmMembers(b) {
			out = append(out, path.Join(path.Dir(lf), m, "node_modules"))
		}
	}
	return out, nil
}

// pnpmMembers are the importers of a pnpm lockfile other than the root that have dependencies of any kind: a member with `{}` (or with
// none listed) gets no node_modules. A key that is not a plain relative path is left out.
func pnpmMembers(lock []byte) []string {
	var out []string
	in, key, deps := false, "", false
	flush := func() {
		if in && key != "" && key != "." && deps && filepath.IsLocal(key) && !strings.ContainsAny(key, `\`) && path.Clean(key) == key {
			out = append(out, key)
		}
		key, deps = "", false
	}
	for _, line := range strings.Split(string(lock), "\n") {
		line = strings.TrimRight(line, "\r ")
		switch {
		case line == "":
		case !strings.HasPrefix(line, " "): // a top-level key
			flush()
			in = line == "importers:"
		case !in:
		case strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   "): // an importer: `  spa:` or `  spa: {}`
			flush()
			k, rest, ok := strings.Cut(strings.TrimSpace(line), ":")
			if ok && strings.TrimSpace(rest) == "" {
				key = strings.Trim(k, `'"`)
			}
		case strings.HasPrefix(line, "    ") && !strings.HasPrefix(line, "     "):
			switch strings.TrimSpace(line) {
			case "dependencies:", "devDependencies:", "optionalDependencies:":
				deps = true
			}
		}
	}
	flush()
	return out
}

// depsOK forgets the failures of the key.
func (e *Exec) depsOK(key string) {
	e.deps.mu.Lock()
	delete(e.deps.fails, key)
	e.deps.mu.Unlock()
}
