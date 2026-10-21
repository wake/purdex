package daemon

import (
	"path/filepath"
	"testing"

	"github.com/wake/purdex/internal/wwd/store"
)

// S3-3a (codex critic): the list a reader is shown leaves out a row of a kind or level this build does not know, but the rules that raise and
// clear alerts must still see its key as raised. wwd started back on an older build, with a row a newer one wrote under the key of a rule
// the older one has, must not raise over it as if nothing were there.
func TestAlertBook_ActiveHasTheKeysOfRowsItDoesNotKnow(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "wwd.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.SetLogf(func(string, ...any) {})
	for _, r := range [][3]string{{"disk_low:/", "disk_low", "push"}, {"swap_growth", "swap_growth", "urgent"}, {"future", "meltdown", "push"}} {
		if _, err := st.DB().Exec(`INSERT INTO alerts (key, kind, level, since_ms, last_ms, cleared_ms, detail) VALUES (?, ?, ?, 1000, 2000, NULL, '')`, r[0], r[1], r[2]); err != nil {
			t.Fatal(err)
		}
	}
	got, err := NewAlertBook(st, "mlab", nil, nil).Active()
	if err != nil || len(got) != 3 || !got["disk_low:/"] || !got["swap_growth"] || !got["future"] {
		t.Fatalf("%v %v", got, err)
	}
	list, err := st.ActiveAlerts()
	if err != nil || len(list) != 1 || list[0].Key != "disk_low:/" {
		t.Fatalf("while the list a reader is shown has only the one it knows: %+v %v", list, err)
	}
}
