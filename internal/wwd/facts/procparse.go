package facts

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"hash/fnv"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// parseProcArgs reads a kern.procargs2 buffer: argc (a native int32), the exec
// path, NUL padding, argc argv strings, then the environment up to the first
// empty string. ok is false for a buffer that does not fit that shape; every
// index is bounded, so a short or corrupt buffer is refused, not a panic.
func parseProcArgs(buf []byte) (args, env []string, ok bool) {
	if len(buf) < 4 {
		return nil, nil, false
	}
	argc := int(int32(binary.NativeEndian.Uint32(buf)))
	if argc <= 0 {
		return nil, nil, false
	}
	area := buf[4:]
	i := bytes.IndexByte(area, 0)
	if i < 0 {
		return nil, nil, false
	}
	for i < len(area) && area[i] == 0 {
		i++
	}
	next := func() (string, bool) {
		if i >= len(area) {
			return "", false
		}
		n := bytes.IndexByte(area[i:], 0)
		if n < 0 {
			return "", false
		}
		s := string(area[i : i+n])
		i += n + 1
		return s, true
	}
	for len(args) < argc {
		s, ok := next()
		if !ok {
			return nil, nil, false
		}
		args = append(args, s)
	}
	for {
		s, ok := next()
		if !ok || s == "" {
			break
		}
		env = append(env, s)
	}
	return args, env, true
}

// sessionRef is the ref of a Claude Code sessionId: "_" + base36(FNV-1a-64 mod
// 36^6), the same function as internal/peers.RefID (a test keeps them equal).
// Duplicated because internal/peers pulls in internal/agent, which wwd must not
// depend on (it has to build for Linux and stay small).
func sessionRef(sessionID string) string {
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	const space = 36 * 36 * 36 * 36 * 36 * 36
	h := fnv.New64a()
	_, _ = h.Write([]byte(sessionID))
	n := h.Sum64() % space
	out := make([]byte, 6)
	for i := 5; i >= 0; i-- {
		out[i] = digits[n%36]
		n /= 36
	}
	return "_" + string(out)
}

// sessionFile is what ~/.claude/sessions/<pid>.json says about a live session.
type sessionFile struct {
	sessionID string
	procStart string
}

// matchesStart reports whether the session file was written by the process
// that started at startSec (a recycled pid is not the session). procStart is
// a UTC ctime (internal/peers ProcStartLayout), so the start is formatted in UTC
// and spacing is normalised before comparing.
func (s sessionFile) matchesStart(startSec int64) bool {
	if s.procStart == "" {
		return false
	}
	want := strings.Join(strings.Fields(time.Unix(startSec, 0).UTC().Format("Mon Jan _2 15:04:05 2006")), " ")
	return strings.Join(strings.Fields(s.procStart), " ") == want
}

// scanSessions reads dir/<pid>.json for every pid. Only files named <number>.json
// are opened (the directory also holds key files); one whose pid field disagrees
// with its name, or that is not JSON, is skipped.
func scanSessions(dir string) map[int]sessionFile {
	out := map[int]sessionFile{}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range ents {
		stem, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(stem)
		if err != nil || pid <= 0 {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var j struct {
			PID       int    `json:"pid"`
			SessionID string `json:"sessionId"`
			ProcStart string `json:"procStart"`
		}
		if json.Unmarshal(b, &j) != nil || j.PID != pid || j.SessionID == "" {
			continue
		}
		out[pid] = sessionFile{sessionID: j.SessionID, procStart: j.ProcStart}
	}
	return out
}
