package run

import (
	"errors"
	"fmt"
	"os"
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
	// ExtraProperties are more systemd properties ("Name=value"), only from extraAllowed: properties that take rights away. It is
	// how a measurement adds a hardening property without a code change; it cannot change what the sandbox sets, add a path, a
	// capability, a group or an environment.
	ExtraProperties []string
}

// extraAllowed is every property ExtraProperties may name, with the values that only take something away. A name alone is not
// enough: PrivateDevices=no or TasksMax=infinity would loosen what the host does by default. None of them sets a user, a group, a
// path that becomes writable or visible, an environment, a capability or a command, and none is one the sandbox sets.
var extraAllowed = map[string]func(v string) bool{
	"ProtectKernelTunables":  oneOf("yes"),
	"ProtectKernelModules":   oneOf("yes"),
	"ProtectKernelLogs":      oneOf("yes"),
	"ProtectControlGroups":   oneOf("yes"),
	"ProtectClock":           oneOf("yes"),
	"ProtectHostname":        oneOf("yes"),
	"ProtectProc":            oneOf("invisible", "ptraceable"),
	"ProcSubset":             oneOf("pid"),
	"PrivateDevices":         oneOf("yes"),
	"LockPersonality":        oneOf("yes"),
	"RestrictNamespaces":     oneOf("yes"),
	"RestrictSUIDSGID":       oneOf("yes"),
	"RestrictRealtime":       oneOf("yes"),
	"MemoryDenyWriteExecute": oneOf("yes"),
	"LimitCORE":              oneOf("0"),
	"TasksMax":               atMost(4096), // systemd's own default is 15% of the pid limit, about 4900 here
	"CPUWeight":              atMost(100),  // 100 is the default weight: a lower one only gives way
	"IOWeight":               atMost(100),
	"ReadOnlyPaths":          absolutePath,
}

func oneOf(ok ...string) func(string) bool {
	return func(v string) bool {
		for _, o := range ok {
			if v == o {
				return true
			}
		}
		return false
	}
}

// atMost accepts a plain number from 1 to max (no "infinity", no suffix, no sign).
func atMost(max int) func(string) bool {
	return func(v string) bool {
		n, err := strconv.Atoi(v)
		return err == nil && n >= 1 && n <= max && strconv.Itoa(n) == v
	}
}

// absolutePath accepts one absolute path, optionally with the "-" systemd reads as "if it exists".
func absolutePath(v string) bool {
	v = strings.TrimPrefix(v, "-")
	return strings.HasPrefix(v, "/") && !strings.Contains(v, "..")
}

var (
	extraName  = regexp.MustCompile(`^[A-Z][A-Za-z0-9]{1,63}$`)
	extraValue = regexp.MustCompile(`^[A-Za-z0-9_./:=%+-]{1,200}$`) // one token: no white space, quote, semicolon or newline
)

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
var unitSafe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`) // the same characters the job marker allows

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
	for _, p := range s.ExtraProperties {
		name, value, ok := strings.Cut(p, "=")
		if !ok || !extraName.MatchString(name) || !extraValue.MatchString(value) {
			return fmt.Errorf("run: the extra property %q is not Name=value", p)
		}
		tightens, known := extraAllowed[name]
		if !known {
			return fmt.Errorf("run: the extra property %s is not one that may be added to the sandbox", name)
		}
		if !tightens(value) {
			return fmt.Errorf("run: %s=%s would not take anything away: only a tightening value is accepted", name, value)
		}
	}
	return nil
}

// checkExtraPaths refuses a start whose extra writable path is not there: systemd would fail to set the unit up (226/NAMESPACE) and
// the failure would look like the task's own. The workspace is made by the runner and always exists.
func (s Sandbox) checkExtraPaths() error {
	for _, p := range s.ReadWritePaths {
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("run: the sandbox's writable path %s: %w", p, err)
		}
	}
	return nil
}

// UnitName is the name of the transient unit a job runs in: "wwd-<origin tag>-<job id>". A job id is only unique within the origin that
// made it, so two origins can send the same one, and a unit name must be unique on the host (the tag is worker/ident.OriginTag of the
// connection, 16 hex characters, safe in a unit name where an alias is not). A run with no origin tag keeps the old "wwd-<job id>": the
// quarantine's markers written before the tag existed name units of that form, and they are still asked about by their stored name.
func UnitName(originTag, jobID string) string {
	if originTag == "" {
		return "wwd-" + jobID
	}
	return "wwd-" + originTag + "-" + jobID
}

func unitName(spec Spec) string { return UnitName(spec.Origin, spec.JobID) }

// exitWrapper runs "$@" and writes its exit status to the file named by $0: the code on the first line, then (WWD usage U3b) what
// the unit's own cgroup says the command used, read after it ended and before the shell exits: the CPU time in microseconds
// (cpu.stat usage_usec), the memory peak in bytes (memory.peak) and (U3b-2) how many processes are still in the unit besides the shell
// (cgroup.procs minus one: a command that left a daemon behind, whose later use these figures do not have). A unit that ended well is unloaded by systemd at once, so
// `systemctl show` cannot give these; the cgroup is still there while this shell runs. They are empty when they cannot be read
// (no cgroup v2, memory.peak before Linux 5.19, no cgroup of its own): the run's usage is then unknown, never 0, and never the root
// cgroup's (the guard below refuses "/"). Only the code decides anything. When the file cannot be written (the workspace is full or
// read-only) it exits 125, not the command's code: a code of the command's own could be read as one of systemd's (200..243), and
// without the file the runner cannot tell what the command did. 125 with no file is therefore an infrastructure failure.
const exitWrapper = `"$@"; rc=$?; cpu=; mem=; left=; cg=$(sed -n 's/^0:://p' /proc/self/cgroup 2>/dev/null); ` +
	`if [ -n "$cg" ] && [ "$cg" != / ] && [ -d "/sys/fs/cgroup$cg" ]; then ` +
	// the processes still in the unit besides this shell, counted with the shell's own read (no fork, so nothing of the wrapper's is in it)
	// before the two reads below fork their helpers: what is left is something the command started and did not end
	`if [ -r "/sys/fs/cgroup$cg/cgroup.procs" ]; then n=0; while read -r _; do n=$((n+1)); done 2>/dev/null < "/sys/fs/cgroup$cg/cgroup.procs"; left=$((n-1)); fi; ` +
	`cpu=$(sed -n 's/^usage_usec //p' "/sys/fs/cgroup$cg/cpu.stat" 2>/dev/null); mem=$(cat "/sys/fs/cgroup$cg/memory.peak" 2>/dev/null); fi; ` +
	`printf '%s\n%s\n%s\n%s\n' "$rc" "$cpu" "$mem" "$left" > "$0" || exit 125; exit "$rc"`

// sandboxArgv is the systemd-run command line (without the leading sudo). The environment is cleared with env -i and then set
// from env, so nothing of the unit's default environment reaches the job.
func sandboxArgv(sb Sandbox, unit, workspace, dir, exitFile string, timeout, killAfter time.Duration, env []string, prog string, args []string) []string {
	argv := []string{"systemd-run", "--wait", "--pipe", "--uid=" + sb.User, "--unit=" + unit, "--slice=" + sb.Slice,
		"-p", "CPUQuota=" + sb.CPUQuota, "-p", "MemoryMax=" + sb.MemoryMax, "-p", "MemorySwapMax=" + sb.MemorySwapMax}
	if timeout > 0 {
		// the fuse that holds even if wwd is gone: the worker's own deadline comes first, one minute earlier
		argv = append(argv, "-p", "RuntimeMaxSec="+strconv.Itoa(ceilSeconds(timeout)+60))
	}
	argv = append(argv, "-p", "TimeoutStopSec="+strconv.Itoa(ceilSeconds(killAfter)), // TERM, then KILL after this: as on macOS
		"-p", "PrivateTmp=yes", "-p", "ProtectHome=read-only",
		// the whole file system read-only but the workspace (ReadWritePaths) and the private /tmp and /var/tmp (WWD-4d-3: it closed
		// writes to /srv/backups, /srv/stacks, /var/crash and the like at no cost that was measured)
		"-p", "ProtectSystem=strict",
		"-p", "ReadWritePaths="+strings.Join(append([]string{workspace}, sb.ReadWritePaths...), " "),
		"-p", "WorkingDirectory="+dir, "-p", "NoNewPrivileges=yes",
		"-p", "InaccessiblePaths="+strings.Join(sb.InaccessiblePaths, " "))
	for _, p := range sb.ExtraProperties {
		argv = append(argv, "-p", p)
	}
	argv = append(argv, "env", "-i")
	argv = append(argv, env...)
	// The command runs under a one-line shell that leaves its exit status in exitFile ($0). A unit that ended well is unloaded by
	// systemd at once, so its Result cannot be read; the file is what tells a job that finished from a unit stopped from outside
	// (both leave systemd-run with exit 0).
	argv = append(argv, "nice", "-n", "10", "ionice", "-c3", "sh", "-c", exitWrapper, exitFile, prog)
	return append(argv, args...)
}

func ceilSeconds(d time.Duration) int {
	n := int(d / time.Second)
	if d%time.Second > 0 {
		n++
	}
	return n
}
