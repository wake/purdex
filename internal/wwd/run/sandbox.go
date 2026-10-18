package run

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Sandbox runs the command as a transient systemd service instead of a process group (spec §5, M-L2, Linux hosts such as
// mserv). A service, never a scope: a scope rejects the sandbox properties. The unit is named wwd-<job id>, so a failed one is
// ours to clear and mserv's failed-unit alert never sees an unnamed run-*.service; its Result is read before reset-failed.
type Sandbox struct {
	User              string   // the account the job runs as (--uid); required
	Slice             string   // default wwd.slice (no "-" in a slice name: a "-" makes a hierarchy)
	CPUQuota          string   // default 200%
	MemoryMax         string   // default 4G (with no MemoryHigh: MemoryMax alone kills at once, MemoryHigh only throttles)
	MemorySwapMax     string   // default 0
	ReadWritePaths    []string // writable besides the workspace (a composer cache)
	InaccessiblePaths []string // hidden besides /run/docker.sock, which is always hidden when it exists
	SudoPath          string   // default "sudo"
	SystemctlPath     string   // default "systemctl" (show needs no root)
}

// the "-" makes a missing path (a host with no docker) harmless: without it systemd refuses to set up the unit's namespace
const dockerSocket = "-/run/docker.sock"

func (s Sandbox) withDefaults() Sandbox {
	def := func(p *string, v string) {
		if *p == "" {
			*p = v
		}
	}
	def(&s.Slice, "wwd.slice")
	def(&s.CPUQuota, "200%")
	def(&s.MemoryMax, "4G")
	def(&s.MemorySwapMax, "0")
	def(&s.SudoPath, "sudo")
	def(&s.SystemctlPath, "systemctl")
	s.InaccessiblePaths = append([]string{dockerSocket}, s.InaccessiblePaths...)
	return s
}

// unitSafe is what a job id may contain to become part of a unit name.
var unitSafe = regexp.MustCompile(`^[A-Za-z0-9_.]{1,100}$`)

func (s Sandbox) validate(spec Spec) error {
	if s.User == "" {
		return errors.New("run: the sandbox needs a user")
	}
	if !unitSafe.MatchString(spec.JobID) {
		return fmt.Errorf("run: job id %q cannot be a unit name", spec.JobID)
	}
	// ReadWritePaths and InaccessiblePaths are lists that systemd splits at white space again: a path with white space would
	// become two paths (or make the unit fail), so it is refused
	for _, p := range append(append([]string{spec.Workspace}, s.ReadWritePaths...), s.InaccessiblePaths...) {
		if strings.ContainsAny(p, " \t\r\n\"'\\") {
			return fmt.Errorf("run: the path %q has white space or a quote: it cannot be a sandbox path", p)
		}
	}
	return nil
}

func unitName(jobID string) string { return "wwd-" + jobID }

// sandboxArgv is the systemd-run command line (without the leading sudo). The environment is cleared with env -i and then set
// from env, so nothing of the unit's default environment reaches the job.
func sandboxArgv(sb Sandbox, unit, workspace, dir string, timeout, killAfter time.Duration, env []string, prog string, args []string) []string {
	argv := []string{"systemd-run", "--wait", "--pipe", "--uid=" + sb.User, "--unit=" + unit, "--slice=" + sb.Slice,
		"-p", "CPUQuota=" + sb.CPUQuota, "-p", "MemoryMax=" + sb.MemoryMax, "-p", "MemorySwapMax=" + sb.MemorySwapMax}
	if timeout > 0 {
		// the fuse that holds even if wwd is gone: the worker's own deadline comes first, one minute earlier
		argv = append(argv, "-p", "RuntimeMaxSec="+strconv.Itoa(ceilSeconds(timeout)+60))
	}
	argv = append(argv, "-p", "TimeoutStopSec="+strconv.Itoa(ceilSeconds(killAfter)), // TERM, then KILL after this: as on macOS
		"-p", "PrivateTmp=yes", "-p", "ProtectHome=read-only",
		"-p", "ReadWritePaths="+strings.Join(append([]string{workspace}, sb.ReadWritePaths...), " "),
		"-p", "WorkingDirectory="+dir, "-p", "NoNewPrivileges=yes",
		"-p", "InaccessiblePaths="+strings.Join(sb.InaccessiblePaths, " "))
	argv = append(argv, "env", "-i")
	argv = append(argv, env...)
	argv = append(argv, "nice", "-n", "10", "ionice", "-c3", prog)
	return append(argv, args...)
}

func ceilSeconds(d time.Duration) int {
	n := int(d / time.Second)
	if d%time.Second > 0 {
		n++
	}
	return n
}
