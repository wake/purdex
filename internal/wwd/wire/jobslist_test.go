package wire

import (
	"strings"
	"testing"
)

// GET /v1/jobs answers the jobs and the origin's cursor: the position after the last change in the answer, to be sent back as ?after= to
// read what changed since (3b-4a). The cursor is a decimal string the client treats as opaque. An empty one is only an answer that is
// not a cursor read; a non-empty one must have the form.
func TestJobsListResponseCarriesTheCursor(t *testing.T) {
	r := roundTrip[JobsListResponse](t, "jobs_list_cursor.json")
	if len(r.Jobs) != 1 || r.Cursor != "42" {
		t.Fatalf("fields lost: %+v", r)
	}
	good := string(fixture(t, "jobs_list_cursor.json"))
	for name, raw := range map[string]string{
		"signed cursor":  strings.Replace(good, `"cursor": "42"`, `"cursor": "-1"`, 1),
		"text cursor":    strings.Replace(good, `"cursor": "42"`, `"cursor": "abc"`, 1),
		"long cursor":    strings.Replace(good, `"cursor": "42"`, `"cursor": "1234567890123456789012"`, 1),
		"numeric cursor": strings.Replace(good, `"cursor": "42"`, `"cursor": 42`, 1),
		"bad job":        strings.Replace(good, `"class": "merge"`, `"class": "urgent"`, 1),
	} {
		if raw == good {
			t.Fatalf("%s: replacement changed nothing", name)
		}
		if _, err := Decode[JobsListResponse]([]byte(raw)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := Decode[JobsListResponse]([]byte(`{"jobs": [], "cursor": ""}`)); err != nil {
		t.Errorf("an answer with no cursor is refused: %v", err)
	}
}

func TestValidCursor(t *testing.T) {
	for _, s := range []string{"0", "1", "42", "999999999999999999"} {
		if !ValidCursor(s) {
			t.Errorf("%q refused", s)
		}
	}
	for _, s := range []string{"", "-1", "+1", "1.5", " 1", "1 ", "0x10", "1234567890123456789"} {
		if ValidCursor(s) {
			t.Errorf("%q accepted", s)
		}
	}
}
