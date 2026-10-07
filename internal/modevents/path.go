// Package modevents is the daemon side of the Purdex mod event channel
// (interface U1 spec §6): a Unix-socket listener that only the daemon's own
// user can connect to, the POST /mod/v1/events ingest, and an in-memory
// registry of mod streams that delivers their events, in order, to
// in-process subscribers.
//
// It imports nothing under internal/agent or internal/module: the agent
// side subscribes to it, never the other way round.
package modevents

import (
	"errors"
	"io/fs"
	"path/filepath"
)

// SocketName is the socket's file name inside the daemon's data dir.
const SocketName = "mod.sock"

// MaxSocketPath is the longest socket path the channel accepts (spec
// §6.1). sun_path holds 104 bytes on darwin and 108 on linux; 100 leaves
// room for the terminator on both.
const MaxSocketPath = 100

// ResolveSocketPath is where the daemon listens and the mod connects:
// mod.sock in dataDir made absolute with its symlinks resolved, the
// directory Listen binds in. The daemon module and the pdx.json writer
// both use it, so pdx.json names the socket the daemon actually binds.
//
// A dataDir that does not exist yet is not resolved: the path is in the
// cleaned absolute dataDir. ok is false when the path is longer than
// MaxSocketPath (the channel is then disabled, there is no fallback
// location), or when dataDir cannot be made absolute or resolved for any
// other reason (Listen would fail there too). The path is returned either
// way so it can be logged and reported.
func ResolveSocketPath(dataDir string) (path string, ok bool) {
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return filepath.Join(dataDir, SocketName), false
	}
	dir, err := filepath.EvalSymlinks(abs)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		dir = abs
	case err != nil:
		return filepath.Join(abs, SocketName), false
	}
	path = filepath.Join(dir, SocketName)
	return path, len(path) <= MaxSocketPath
}
