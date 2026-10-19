package store

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// v7 (WWD-8 8b-3): an exception may carry allow and flags (spec §13.4), kept in two columns that are NULL for an exception made before.
// A wwd that predates v7 refuses the file (a newer schema), so a roll back restores the backup taken before the upgrade.

func hold() wire.Exception {
	until := t0.Add(2 * time.Hour)
	return wire.Exception{
		Name: "release-hold", Until: &until, Holds: []wire.Hold{wire.HoldHeavy, wire.HoldLight},
		Allow: &wire.HoldAllow{Classes: []wire.JobClass{wire.ClassRelease}, Repos: []string{"purdex"}},
		Flags: []wire.HoldFlag{wire.HoldFlagRelease, wire.HoldFlagExpectPressure},
	}
}

// A new file has the columns; allow and flags survive a reopen exactly (an empty allow, which admits nothing, is not an absent one).
func TestHoldFieldsPersistAcrossReopen(t *testing.T) {
	s, p := open(t)
	cols := tableCols(t, s, "exceptions")
	if !cols["allow_json"] || !cols["flags"] {
		t.Fatalf("columns: %v", cols)
	}
	until := t0.Add(time.Hour)
	empty := Stored{Exception: wire.Exception{Name: "quiet", Until: &until, Holds: []wire.Hold{wire.HoldHeavy}, Allow: &wire.HoldAllow{Classes: []wire.JobClass{}, Repos: []string{}}}, Since: t0}
	full := Stored{Exception: hold(), Since: t0.Add(time.Minute)}
	plain := Stored{Exception: wire.Exception{Name: "plain", Until: &until, Holds: []wire.Hold{wire.HoldHeavy}}, Since: t0.Add(2 * time.Minute)}
	for _, e := range []Stored{empty, full, plain} {
		if err := s.SaveException(e); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	s2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	got, err := s2.LoadExceptions()
	if err != nil || len(got) != 3 {
		t.Fatalf("%+v %v", got, err)
	}
	if a := got[0].Exception.Allow; a == nil || len(a.Classes) != 0 || len(a.Repos) != 0 || got[0].Exception.Flags != nil {
		t.Errorf("an empty allow must stay present and empty: %+v", got[0].Exception)
	}
	if !reflect.DeepEqual(got[1].Exception.Allow, full.Exception.Allow) || !reflect.DeepEqual(got[1].Exception.Flags, full.Exception.Flags) {
		t.Errorf("allow and flags: %+v", got[1].Exception)
	}
	if got[2].Exception.Allow != nil || got[2].Exception.Flags != nil {
		t.Errorf("an exception with neither must read back with neither: %+v", got[2].Exception)
	}
	// replacing a hold by a plain exception of the same name clears the fields
	back := Stored{Exception: wire.Exception{Name: "release-hold", Until: &until, Holds: []wire.Hold{wire.HoldHeavy}}, Since: t0.Add(time.Minute)}
	if err := s2.SaveException(back); err != nil {
		t.Fatal(err)
	}
	got, _ = s2.LoadExceptions()
	for _, g := range got {
		if g.Exception.Name == "release-hold" && (g.Exception.Allow != nil || g.Exception.Flags != nil) {
			t.Errorf("the old fields stayed: %+v", g.Exception)
		}
	}
}

// The v6 file (what 760 deploys: host_schema 6, an exception made the old way) gains the columns on open, keeps its row with the old
// meaning, and opening it again changes nothing.
func TestOpenMigratesV6ToV7(t *testing.T) {
	p := filepath.Join(t.TempDir(), "wwd.db")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`ALTER TABLE exceptions DROP COLUMN allow_json`, `ALTER TABLE exceptions DROP COLUMN flags`,
		`INSERT INTO exceptions (name, until_ms, pid, pid_ident, holds, since_ms) VALUES ('old', 4102444800000, NULL, '', '["heavy"]', 1000)`,
		`UPDATE host_schema SET version = 6`,
	} {
		if _, err := s.DB().Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	for round := 1; round <= 2; round++ {
		s, err := Open(p)
		if err != nil {
			t.Fatalf("open %d: %v", round, err)
		}
		cols := tableCols(t, s, "exceptions")
		if !cols["allow_json"] || !cols["flags"] {
			t.Fatalf("open %d: columns %v", round, cols)
		}
		got, err := s.LoadExceptions()
		if err != nil || len(got) != 1 || got[0].Exception.Name != "old" || got[0].Exception.Allow != nil || got[0].Exception.Flags != nil ||
			!reflect.DeepEqual(got[0].Exception.Holds, []wire.Hold{wire.HoldHeavy}) {
			t.Fatalf("open %d: the old row keeps its meaning: %+v %v", round, got, err)
		}
		var v int
		if err := s.DB().QueryRow(`SELECT version FROM host_schema`).Scan(&v); err != nil || v != schemaVersion {
			t.Fatalf("open %d: version %d %v", round, v, err)
		}
		s.Close()
	}
}
