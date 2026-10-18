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

// The answer names the database its cursor belongs to; a non-empty epoch must have the form, an empty one is a server that predates it.
func TestJobsListResponseCarriesTheEpoch(t *testing.T) {
	r := roundTrip[JobsListResponse](t, "jobs_list_cursor.json")
	if r.Epoch != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("epoch lost: %+v", r)
	}
	good := string(fixture(t, "jobs_list_cursor.json"))
	for name, ep := range map[string]string{"short": "abc", "upper": "0123456789ABCDEF0123456789ABCDEF", "long": strings.Repeat("a", 33), "text": strings.Repeat("g", 32)} {
		raw := strings.Replace(good, `"epoch": "0123456789abcdef0123456789abcdef"`, `"epoch": "`+ep+`"`, 1)
		if raw == good {
			t.Fatalf("%s: replacement changed nothing", name)
		}
		if _, err := Decode[JobsListResponse]([]byte(raw)); err == nil {
			t.Errorf("a %s epoch was accepted", name)
		}
	}
	if _, err := Decode[JobsListResponse]([]byte(`{"jobs": [], "cursor": "0", "epoch": ""}`)); err != nil {
		t.Errorf("an answer from a server with no epoch is refused: %v", err)
	}
}

// A cursor past the origin's last change is a 409 that says where the origin is and which database it is.
func TestCursorAhead(t *testing.T) {
	c := roundTrip[CursorAhead](t, "cursor_ahead.json")
	if c.Head != "17" || c.Reason != CursorAheadReason {
		t.Fatalf("fields lost: %+v", c)
	}
	good := string(fixture(t, "cursor_ahead.json"))
	for name, raw := range map[string]string{
		"other reason": strings.Replace(good, `"cursor_ahead"`, `"locks_busy"`, 1),
		"bad head":     strings.Replace(good, `"head": "17"`, `"head": "x"`, 1),
		"no epoch":     strings.Replace(good, `"epoch": "0123456789abcdef0123456789abcdef"`, `"epoch": ""`, 1),
		"unknown key":  strings.Replace(good, `"head"`, `"extra": 1, "head"`, 1),
	} {
		if raw == good {
			t.Fatalf("%s: replacement changed nothing", name)
		}
		if _, err := Decode[CursorAhead]([]byte(raw)); err == nil {
			t.Errorf("%s was accepted", name)
		}
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
