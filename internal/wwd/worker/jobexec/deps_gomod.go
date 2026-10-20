package jobexec

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/wake/purdex/internal/execstat"
	"github.com/wake/purdex/internal/wwd/run"
	"github.com/wake/purdex/internal/wwd/snapshot"
	"github.com/wake/purdex/internal/wwd/wire"
	"github.com/wake/purdex/internal/wwd/worker/qos"
)

// downloadEnv are the names of the task's environment that reach the download: how to get out to the network, and nothing else. The task's
// manifest comes with the snapshot, and the download runs with the worker's credentials outside the sandbox, so a name such as
// GIT_SSH_COMMAND, GIT_CONFIG_GLOBAL, GOENV or HOME would let a snapshot run a program of its own with them.
var downloadEnv = map[string]bool{
	"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true, "http_proxy": true, "https_proxy": true, "no_proxy": true,
}

// downloadModules fills the module cache (<dir of go.sum>/.gomodcache in the job's workspace) with the modules go.mod and go.sum name. It is
// the worker's own work: outside the job's sandbox, with the worker's HOME and environment, so that git reaches the private host with the
// configuration and keys of the worker. The job never gets those; it is given the finished cache and told to use nothing else (depsJobEnv).
// The fixed values of the task's environment reach the download (a proxy setting, say), names taken from the worker's environment do not
// beyond what the worker already has. The cache is written writable (-modcacherw), so that the workspace can be removed afterwards.
func (e *Exec) downloadModules(ctx context.Context, job wire.Job, ws snapshot.Workspace, lockfile string, in installer, taskEnv []string) error {
	dir := filepath.Join(ws.Dir, filepath.FromSlash(path.Dir(lockfile)))
	cacheDir := filepath.Join(dir, ".gomodcache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return err
	}
	prog, err := run.LookPath(in.argv[0], "", e.cfg.ParentEnv)
	if err != nil {
		return fmt.Errorf("jobexec: %s is not on the job PATH: %w", in.argv[0], err)
	}
	argv, _ := qos.For(job.Class, e.cfg.BackgroundDev, append([]string{prog}, in.argv[1:]...))
	cctx, cancel := context.WithTimeout(ctx, depsInstallTimeout)
	defer cancel()
	cmd := execstat.CommandContext(cctx, "wwd.jobexec.gomod", argv[0], argv[1:]...)
	cmd.Dir = dir
	env := append([]string(nil), e.cfg.ParentEnv...)
	for _, kv := range taskEnv {
		if name, _, ok := strings.Cut(kv, "="); ok && downloadEnv[name] {
			env = append(env, kv)
		}
	}
	// last, so that nothing before can point the download elsewhere: the later value of a name is the one the child gets
	cmd.Env = append(env, "GOMODCACHE="+cacheDir, "GOPRIVATE="+goPrivate, "GOFLAGS=-modcacherw", "GOTOOLCHAIN=local", "GOWORK=off")
	// its own process group, so a cancel takes git and ssh with it
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("install %s: %w%s", lockfile, err, installTail(out.Bytes()))
	}
	return nil
}
