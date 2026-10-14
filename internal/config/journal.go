package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/BurntSushi/toml"
)

// JournalConfig is the [journal] section (docs/specs/2026-10-14-workbook-journal-spec.md §3, plan J-1d). It is edited in
// config.toml only: it is not a field of PUT /api/config, because it decides which repositories the daemon pushes to.
type JournalConfig struct {
	// PushProjects lists the projects whose journal branch (purdex/journal) is pushed, as `host/owner/repo`
	// (`github.com/wake/purdex`), matched against the normalised remote.origin.url. Empty or absent (the default): every
	// journal stays local. Read again at every push round, so an edit applies without a restart.
	PushProjects []string `toml:"push_projects" json:"push_projects"`
}

// JournalPushProjects is the configured push allow-list as loaded at start; nil when the section is absent.
func (c Config) JournalPushProjects() []string {
	if c.Journal == nil {
		return nil
	}
	return c.Journal.PushProjects
}

// ReadJournalPushProjects reads [journal] push_projects from the config file at path ("" = the default file) as it is now,
// ignoring every other section: a daemon does not reload its config, but a push round must see an edit (plan T-1d.4). A
// missing file or section is an empty list; an unreadable file is an error and no list.
func ReadJournalPushProjects(path string) ([]string, error) {
	if path == "" {
		path = filepath.Join(defaults().DataDir, "config.toml")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}
	var doc struct {
		Journal *JournalConfig `toml:"journal"`
	}
	if err := toml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if doc.Journal == nil {
		return nil, nil
	}
	return slices.Clone(doc.Journal.PushProjects), nil
}
