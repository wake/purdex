package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"time"
)

// LockWait bounds how long a writer waits for "<path>.lock": a holder that never lets go makes the write fail, it does not
// hang the daemon (UpdateConfig holds CfgMu while it writes).
var LockWait = 5 * time.Second

// canonPath is the one name every writer uses for a config file: absolute, with symlinks resolved (the file itself if it
// exists, else its nearest resolvable directory), so two names of one file name one lock file and a symlinked config stays a
// symlink — the write goes to what it points at. Known limit: a config reached through a dangling symlink whose target holds
// ".." after another symlink, or through a dangling symlink to a directory, is not resolved component by component.
func canonPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	for hops := 0; hops < 16; hops++ {
		if real, err := filepath.EvalSymlinks(abs); err == nil {
			return real
		}
		// A dangling symlink as the last element: follow it by hand, so the name is where it would be created.
		if st, err := os.Lstat(abs); err == nil && st.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(abs)
			if err != nil {
				return abs
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(abs), target)
			}
			abs = filepath.Clean(target)
			continue
		}
		// Not there yet: the deepest ancestor that exists, resolved, with the rest appended.
		var rest []string
		dir := abs
		for {
			rest = append([]string{filepath.Base(dir)}, rest...)
			parent := filepath.Dir(dir)
			if parent == dir {
				return abs
			}
			dir = parent
			if real, err := filepath.EvalSymlinks(dir); err == nil {
				return filepath.Join(append([]string{real}, rest...)...)
			}
		}
	}
	return abs
}

// afterCanon is a test seam: called once WithLock has resolved its path, before it waits for the lock.
var afterCanon func()

// WithLock runs fn holding the exclusive lock every writer of the config file at path takes (waiting at most LockWait). A
// caller that holds other locks of its own (the daemon's CfgMu) takes this one FIRST, so waiting for another process never
// holds them; inside fn it writes with UpdateChangedHeld. fn is given the path that was locked (the config's canonical name,
// resolved once): it must read and write THAT path, not resolve its own again, which a retargeted symlink could point
// elsewhere. Not reentrant: a nested WithLock/Update on the same file waits out LockWait and fails.
func WithLock(path string, fn func(locked string) error) error {
	locked := canonPath(path)
	if afterCanon != nil {
		afterCanon()
	}
	return withFileLock(locked, func() error { return fn(locked) })
}

// withFileLock runs fn holding an exclusive flock on "<path>.lock", the one lock every writer of the config file takes
// (Update, SetToken, and the daemon's start-up gate). It waits at most LockWait.
func withFileLock(path string, fn func() error) error {
	if err := os.MkdirAll(dirOf(path), 0755); err != nil {
		return err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	deadline := time.Now().Add(LockWait)
	for {
		err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return fmt.Errorf("config lock %s.lock: %w", path, err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("config lock %s.lock: still held after %s", path, LockWait)
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	return fn()
}

// Update is the one protocol every writer of config.toml shares (#2556): under "<path>.lock" it reads the FILE (a missing one
// is the defaults), lets mutate change what the caller owns, and writes the result atomically (temp file of its own, 0600). The
// base is the file and not the writer's memory, so what another writer put there — a token `pdx token generate` set under a
// running daemon, a host added by a second process — is never written back to an older value. Comments and keys Config does not
// know are not kept (as with WriteFile); a token-only change with SetToken keeps them. A mutate error is returned as it is and
// writes nothing. The returned Config is what was written.
func Update(path string, mutate func(*Config) error) (Config, error) {
	path = canonPath(path)
	var out Config
	err := withFileLock(path, func() error {
		cfg, err := Load(path)
		if err != nil {
			return err
		}
		if err := mutate(&cfg); err != nil {
			return err
		}
		if err := WriteFile(path, cfg); err != nil {
			return err
		}
		out = cfg
		return nil
	})
	return out, err
}

// UpdateChanged is Update for a writer that already has the change as a before/after pair of its own config (the daemon's
// UpdateConfig: its mutate runs once, on its memory, where it validates and may decide things): only the fields that differ
// between before and after are written onto the file as it is under the lock — the other fields, which another writer may
// have changed, stay as the file has them. A field that both the file and this writer changed takes this writer's value.
func UpdateChanged(path string, before, after Config) (Config, error) {
	return Update(path, func(onDisk *Config) error {
		applyChanged(reflect.ValueOf(onDisk).Elem(), reflect.ValueOf(before), reflect.ValueOf(after))
		return nil
	})
}

// UpdateChangedHeld is UpdateChanged for a caller that already holds the file's lock (inside WithLock): path is the locked
// path WithLock handed to it, used as it is.
func UpdateChangedHeld(path string, before, after Config) (Config, error) {
	cfg, err := Load(path)
	if err != nil {
		return Config{}, err
	}
	applyChanged(reflect.ValueOf(&cfg).Elem(), reflect.ValueOf(before), reflect.ValueOf(after))
	if err := WriteFile(path, cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// applyChanged sets in dst every field of after that differs from before: a struct is compared field by field (so a change to
// peers.deliver does not carry peers.hosts along), anything else — scalar, slice, map, pointer — as a whole. Fields that are
// not persisted (toml:"-") are left alone.
func applyChanged(dst, before, after reflect.Value) {
	t := dst.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() || f.Tag.Get("toml") == "-" {
			continue
		}
		b, a := before.Field(i), after.Field(i)
		if reflect.DeepEqual(b.Interface(), a.Interface()) {
			continue
		}
		if f.Type.Kind() == reflect.Struct {
			applyChanged(dst.Field(i), b, a)
			continue
		}
		dst.Field(i).Set(a)
	}
}
