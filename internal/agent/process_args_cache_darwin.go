package agent

import (
	"bytes"
	"slices"
	"sync"
	"time"
)

// A process whose arguments the kernel read cannot reproduce byte for byte (a non-ASCII argv, another user's process, a zombie) is read
// through two ps forks, comm and args (#3316). The shared process snapshot (ReadFresh) keeps no answer, so a registry read that comes
// several times a second asked ps again each time: on the Mac mini one watcher whose launch prompt is Chinese cost about 45 forks a minute.
// psArgsCache keeps ps's answer for such a process, and only for it: an argv the fast path takes never comes here.
//
// The key is the process itself, not its pid: the pid, the kernel's full start time (what the snapshot saw) and the 16-character command
// name the kernel holds, which an exec changes while the pid and the start time stay. procArgsPlatform still compares the start time with
// the process that is there now after every read, a hit included, so a pid taken by another process finds no entry and is told apart as
// before (ErrProcessChanged).
//
// What the cache can get wrong is a process that changes its argument area without changing its 16-character command name, which
// ps would then show differently. setproctitle does that (node's process.title writes the area; it happens at start-up, before the first
// registry read can see the process), so the answer is kept at most psArgsCacheTTL and the cache holds at most psArgsCacheMax entries.
// An answer is never kept when ps failed.
const (
	psArgsCacheTTL = 60 * time.Second
	psArgsCacheMax = 64
)

type psArgsKey struct {
	pid  int
	id   procIdentity
	comm string
}

type psArgsEntry struct {
	exePath string
	argv    []string
	at      time.Time
}

var (
	psArgsMu    sync.Mutex
	psArgsStore = map[psArgsKey]psArgsEntry{}
	// psArgsNow is the cache's clock; tests move it.
	psArgsNow = time.Now
)

func psArgsGet(k psArgsKey) (string, []string, bool) {
	psArgsMu.Lock()
	defer psArgsMu.Unlock()
	e, ok := psArgsStore[k]
	if !ok {
		return "", nil, false
	}
	if psArgsNow().Sub(e.at) >= psArgsCacheTTL {
		delete(psArgsStore, k)
		return "", nil, false
	}
	// A copy, so a caller that edits its Argv cannot change what the next hit returns.
	return e.exePath, slices.Clone(e.argv), true
}

func psArgsPut(k psArgsKey, exePath string, argv []string) {
	psArgsMu.Lock()
	defer psArgsMu.Unlock()
	now := psArgsNow()
	if len(psArgsStore) >= psArgsCacheMax {
		for old, e := range psArgsStore {
			if now.Sub(e.at) >= psArgsCacheTTL {
				delete(psArgsStore, old)
			}
		}
		if len(psArgsStore) >= psArgsCacheMax {
			// All of them are current. The cache is a saving, not state: start over rather than choose a victim.
			clear(psArgsStore)
		}
	}
	psArgsStore[k] = psArgsEntry{exePath: exePath, argv: slices.Clone(argv), at: now}
}

// psArgsReset empties the cache; tests use it so that one test's answer is not the next one's hit.
func psArgsReset() {
	psArgsMu.Lock()
	defer psArgsMu.Unlock()
	clear(psArgsStore)
}

// kernComm is the kernel's command name of the process (p_comm), up to its NUL.
func kernComm(comm []byte) string {
	if i := bytes.IndexByte(comm, 0); i >= 0 {
		comm = comm[:i]
	}
	return string(comm)
}
