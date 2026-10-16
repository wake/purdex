package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// JournalConfig is the [journal] section (docs/specs/2026-10-14-workbook-journal-spec.md §3, plan J-1d / J-1n). It is edited
// in config.toml only: it is not a field of PUT /api/config, because it decides which repositories the daemon pushes to.
// Every key may be left out.
type JournalConfig struct {
	// Enabled turns the whole journal on or off. Absent (nil) = on. Off: no journal job is queued (no second model call
	// after a turn), the drainer, the task mirror and the git loop are not started, the journal API answers 503 and the
	// daemon does not announce journal.v1. Read at start: a change applies after a restart.
	Enabled *bool `toml:"enabled,omitempty" json:"enabled,omitempty"`
	// PushProjects lists the projects whose journal branch (purdex/journal) is pushed, as `host/owner/repo`
	// (`github.com/wake/purdex`), matched against the normalised remote.origin.url. Empty or absent (the default): every
	// journal stays local. Read again at every push round, so an edit applies without a restart.
	PushProjects []string `toml:"push_projects" json:"push_projects"`
	// PersonName is how the journal calls the person it is written for (the journal job's person_name, the label of a
	// request they make). Empty / absent = DefaultJournalPersonName. Read where the journal job's input is built, so
	// a reload of the config applies to the next job.
	PersonName string `toml:"person_name,omitempty" json:"person_name,omitempty"`
}

// DefaultJournalPersonName is the person's name in the journal when [journal] person_name is not set. It equals
// journal.DefaultPersonName (a test pins the two together).
const DefaultJournalPersonName = "使用者"

// JournalEnabled says whether the journal runs: on unless [journal] enabled is false.
func (c Config) JournalEnabled() bool {
	return c.Journal == nil || c.Journal.Enabled == nil || *c.Journal.Enabled
}

// JournalPersonName is the configured person name, trimmed, or the default when it is unset or blank.
func (c Config) JournalPersonName() string {
	if c.Journal != nil {
		if n := strings.TrimSpace(c.Journal.PersonName); n != "" {
			return n
		}
	}
	return DefaultJournalPersonName
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
