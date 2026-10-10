package config

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"syscall"

	"github.com/BurntSushi/toml"
)

// SetToken writes token as the top-level `token` of the config file at path (#1950).
//
// The file is edited by that one line: comments, unknown keys and tables stay byte for byte (a rewrite from Config drops all of
// them). When a line edit is not safe — a multi-line string before the first table, or the edited text does not read back as the
// token — it falls back to loading the file and writing it whole. A missing file is created from the defaults. Either way the
// write goes through a temp file of its own, mode 0600, and the whole read-modify-write holds an exclusive lock on "<path>.lock",
// so two daemons sharing a config, or `pdx token generate` beside a starting daemon, cannot lose each other's update.
func SetToken(path, token string) error {
	if err := os.MkdirAll(dirOf(path), 0755); err != nil {
		return err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		if out, ok := withTopLevelToken(raw, token); ok {
			var check Config
			if toml.Unmarshal(out, &check) == nil && check.Token == token {
				return writeFileAtomic(path, out)
			}
		}
	}
	cfg, err := Load(path) // defaults for a missing file
	if err != nil {
		return err
	}
	cfg.Token = token
	return WriteFile(path, cfg)
}

func dirOf(path string) string {
	if i := strings.LastIndexByte(path, '/'); i > 0 {
		return path[:i]
	}
	return "."
}

// withTopLevelToken returns raw with its top-level `token = "…"` line set to token (inserted before the first table, or appended,
// when there is none). ok is false when raw is not a shape a line edit can handle: a multi-line string in the top-level part, or
// more than one token line.
func withTopLevelToken(raw []byte, token string) (out []byte, ok bool) {
	lines := bytes.SplitAfter(raw, []byte("\n"))
	if n := len(lines); n > 0 && len(lines[n-1]) == 0 {
		lines = lines[:n-1]
	}
	end := len(lines) // the top-level part is lines[:end]
	for i, l := range lines {
		if t := bytes.TrimSpace(l); len(t) > 0 && t[0] == '[' {
			end = i
			break
		}
	}
	idx := -1
	for i := 0; i < end; i++ {
		l := lines[i]
		if bytes.Contains(l, []byte(`"""`)) || bytes.Contains(l, []byte(`'''`)) {
			return nil, false
		}
		t := bytes.TrimLeft(l, " \t")
		if bytes.HasPrefix(t, []byte("token")) && bytes.HasPrefix(bytes.TrimLeft(t[len("token"):], " \t"), []byte("=")) {
			if idx >= 0 {
				return nil, false
			}
			idx = i
		}
	}
	line := []byte(`token = "` + token + "\"\n")
	var b bytes.Buffer
	switch {
	case idx >= 0:
		for i, l := range lines {
			if i == idx {
				b.Write(line)
			} else {
				b.Write(l)
			}
		}
	default:
		for i, l := range lines {
			if i == end {
				b.Write(line)
			}
			b.Write(l)
		}
		if end == len(lines) {
			if n := b.Len(); n > 0 && b.Bytes()[n-1] != '\n' {
				b.WriteByte('\n')
			}
			b.Write(line)
		}
	}
	return b.Bytes(), true
}
