package agent

import (
	"context"
	"errors"
)

// Linux has no table read yet; these keep the package building, and every
// snapshot fails with this error until it does.
var errSnapshotNotImplemented = errors.New("process snapshot: not implemented on linux yet")

func snapshotProcessesPlatform(ctx context.Context) (map[int]*snapshotEntry, error) {
	return nil, errSnapshotNotImplemented
}

func procArgsPlatform(pid int, e *snapshotEntry) (string, []string, error) {
	return "", nil, errSnapshotNotImplemented
}
