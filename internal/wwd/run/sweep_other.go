//go:build !darwin

package run

// DefaultProcSource is none off macOS: on Linux the job runs in its own systemd service and `systemctl stop` ends the whole
// cgroup (spec §5), so there is nothing to sweep by mark.
func DefaultProcSource() ProcSource { return nil }
