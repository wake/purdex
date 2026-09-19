package profiles

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	"github.com/wake/purdex/internal/core"
)

// dbFileMode is owner-only: a profile's `hosts` section carries host tokens
// (spec §7), so this is the one database in DataDir whose mode matters.
const dbFileMode = 0o600

// Module serves sync profiles over /api/profiles/*.
type Module struct {
	core  *core.Core
	store *Store

	// broadcast announces an applied section write to every connected client.
	// It is injected so the handlers can be tested without a core; nil means
	// "do not broadcast".
	broadcast func(eventType, value string)
}

// New returns a new Module ready for registration.
func New() *Module { return &Module{} }

func (m *Module) Name() string           { return "profiles" }
func (m *Module) Dependencies() []string { return nil }

// Init opens (or creates) the profiles SQLite database inside DataDir, owner
// read/write only, and wires the handlers' broadcast to the core's host events.
func (m *Module) Init(c *core.Core) error {
	m.core = c
	path := filepath.Join(c.Cfg.DataDir, "profiles.db")

	// Create the file with the right mode before sqlite does it with 0644, so
	// there is no window in which it is readable — and sqlite gives the -wal
	// and -shm siblings the mode of the main file.
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, dbFileMode)
	if err != nil {
		return fmt.Errorf("create profiles db: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("create profiles db: %w", err)
	}

	store, err := OpenStore(path)
	if err != nil {
		return err
	}
	// O_CREATE's mode only applies to a file it creates (and is masked by the
	// umask, which can only tighten it): chmod covers a database — and
	// siblings — left behind with a looser mode.
	if err := restrictDBFiles(path); err != nil {
		store.Close()
		return err
	}
	m.store = store

	// The event is not about a tmux session, so session is "" — the same
	// convention as backup:done.
	m.broadcast = func(eventType, value string) {
		if m.core != nil && m.core.Events != nil {
			m.core.Events.Broadcast("", eventType, value)
		}
	}
	return nil
}

// restrictDBFiles chmods the database and whichever of its WAL siblings exist.
func restrictDBFiles(path string) error {
	if err := os.Chmod(path, dbFileMode); err != nil {
		return fmt.Errorf("chmod profiles db: %w", err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Chmod(path+suffix, dbFileMode); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("chmod profiles db%s: %w", suffix, err)
		}
	}
	return nil
}

// Start logs a banner; no background work required.
func (m *Module) Start(_ context.Context) error {
	log.Println("[profiles] endpoints enabled")
	return nil
}

// Stop closes the underlying SQLite database.
func (m *Module) Stop(_ context.Context) error {
	if m.store != nil {
		return m.store.Close()
	}
	return nil
}
