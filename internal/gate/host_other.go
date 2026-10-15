//go:build !darwin

package gate

import "errors"

var errNoSysctl = errors.New("the memory pressure sysctls exist only on macOS")

// Elsewhere there is no kernel pressure level to read: the callers treat an error as unreadable (the gate then admits
// after three failed reads, and the parallelism module takes its cores alone).

func sysctlUint64(string) (uint64, error) { return 0, errNoSysctl }
func sysctlUint32(string) (uint32, error) { return 0, errNoSysctl }
