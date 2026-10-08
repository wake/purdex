package team

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestTaskDisplayID(t *testing.T) {
	cases := []struct {
		team string
		seq  int
		want string
	}{
		{"3f2a9c01-5b6d-4e7f-8a90-1b2c3d4e5f60", 1, "3f2a9c-1"},
		{"3F2A9C01-5B6D-4E7F-8A90-1B2C3D4E5F60", 12, "3f2a9c-12"},
		{"abcdef", 7, "abcdef-7"},
	}
	for _, c := range cases {
		if got := TaskDisplayID(c.team, c.seq); got != c.want {
			t.Errorf("TaskDisplayID(%q, %d) = %q, want %q", c.team, c.seq, got, c.want)
		}
	}
}

func TestParseTaskID_Table(t *testing.T) {
	const tm = "3f2a9c01-5b6d-4e7f-8a90-1b2c3d4e5f60"
	cases := []struct {
		id      string
		wantSeq int
		wantOK  bool
	}{
		{"3f2a9c-1", 1, true},
		{"3f2a9c-42", 42, true},
		{"3f2a9c-2147483647", 2147483647, true},
		{"3F2A9C-1", 0, false},                    // the display id is lower case
		{"3f2a9d-1", 0, false},                    // another team's prefix
		{"3f2a9c-0", 0, false},                    // seq starts at 1
		{"3f2a9c-01", 0, false},                   // no leading zeros
		{"3f2a9c-+1", 0, false},                   // no sign
		{"3f2a9c--1", 0, false},                   // no sign
		{"3f2a9c- 1", 0, false},                   // no whitespace
		{" 3f2a9c-1", 0, false},                   // no whitespace
		{"3f2a9c-1 ", 0, false},                   // no whitespace
		{"3f2a9c-1\n", 0, false},                  // no whitespace
		{"3f2a9c-", 0, false},                     // no seq
		{"3f2a9c", 0, false},                      // no dash
		{"-1", 0, false},                          // no prefix
		{"", 0, false},                            // empty
		{"3f2a9c-1-2", 0, false},                  // two dashes
		{"3f2a9c-1x", 0, false},                   // trailing junk
		{"3f2a9c-99999999999999999999", 0, false}, // overflow
		{"3f2a9c01-1", 0, false},                  // not the 6-char prefix
	}
	for _, c := range cases {
		seq, ok := ParseTaskID(c.id, tm)
		if ok != c.wantOK || seq != c.wantSeq {
			t.Errorf("ParseTaskID(%q) = (%d, %v), want (%d, %v)", c.id, seq, ok, c.wantSeq, c.wantOK)
		}
	}
}

func TestParseTaskID_RoundTripsAndRejectsAnotherTeam(t *testing.T) {
	a := "3f2a9c01-5b6d-4e7f-8a90-1b2c3d4e5f60"
	b := "3f2a9c99-0000-4000-8000-000000000000" // shares the first 6 hex chars
	id := TaskDisplayID(a, 5)
	if seq, ok := ParseTaskID(id, a); !ok || seq != 5 {
		t.Fatalf("round trip = (%d, %v)", seq, ok)
	}
	// Same prefix: the id parses for both teams; the store scopes by team id.
	if seq, ok := ParseTaskID(id, b); !ok || seq != 5 {
		t.Fatalf("a team sharing the prefix parses the same seq, got (%d, %v)", seq, ok)
	}
	if _, ok := ParseTaskID(id, "ffffff01-0000-4000-8000-000000000000"); ok {
		t.Fatal("another team's prefix must not parse")
	}
}

// The syntax of a display id needs no team id: six lower-case hex chars, a
// dash, a positive decimal seq without sign or leading zero that fits an int.
func TestParseTaskIDSyntax_Table(t *testing.T) {
	cases := []struct {
		id         string
		wantPrefix string
		wantSeq    int
		wantOK     bool
	}{
		{"3f2a9c-1", "3f2a9c", 1, true},
		{"deadbe-2147483647", "deadbe", 2147483647, true},
		{"3F2A9C-1", "", 0, false},
		{"3f2a9g-1", "", 0, false}, // not hex
		{"3f2a9-1", "", 0, false},  // five chars
		{"3f2a9c0-1", "", 0, false},
		{"3f2a9c-0", "", 0, false},
		{"3f2a9c-01", "", 0, false},
		{"3f2a9c-+1", "", 0, false},
		{"3f2a9c--1", "", 0, false},
		{"3f2a9c-1 ", "", 0, false},
		{"3f2a9c-1\n", "", 0, false},
		{"3f2a9c-", "", 0, false},
		{"garbage", "", 0, false},
		{"x-1", "", 0, false},
		{"", "", 0, false},
		{"3f2a9c-99999999999999999999", "", 0, false},
	}
	for _, c := range cases {
		prefix, seq, ok := ParseTaskIDSyntax(c.id)
		if ok != c.wantOK || prefix != c.wantPrefix || seq != c.wantSeq {
			t.Errorf("ParseTaskIDSyntax(%q) = (%q, %d, %v), want (%q, %d, %v)", c.id, prefix, seq, ok, c.wantPrefix, c.wantSeq, c.wantOK)
		}
	}
}

func TestValidTaskSubject_PeerSafeText(t *testing.T) {
	ok := []string{"x", "fix the thing", strings.Repeat("a", 80), strings.Repeat("字", 80)}
	for _, s := range ok {
		if err := ValidTaskSubject(s); err != nil {
			t.Errorf("ValidTaskSubject(%d runes) = %v, want nil", len([]rune(s)), err)
		}
	}
	bad := map[string]string{
		"empty":        "",
		"81 runes":     strings.Repeat("a", 81),
		"81 CJK runes": strings.Repeat("字", 81),
		"newline":      "a\nb",
		"cr":           "a\rb",
		"trailing nl":  "a\n",
		"bad utf8":     "a\xffb",
	}
	for name, s := range bad {
		if err := ValidTaskSubject(s); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	for name, s := range controlSamples() {
		if err := ValidTaskSubject("a" + s + "b"); err == nil {
			t.Errorf("subject with %s: want an error", name)
		}
	}
	if err := ValidTaskSubject("ok\x1b[2Jspoof"); err == nil {
		t.Error("an ANSI escape in a subject must be refused")
	}
	// Rune counting is unchanged: 80 runes of three-byte CJK pass, 81 do not.
	if ValidTaskSubject(strings.Repeat("字", 80)) != nil || ValidTaskSubject(strings.Repeat("字", 81)) == nil {
		t.Error("rune counting broke")
	}
}

// controlSamples are control characters (C0, DEL and C1) a peer message
// must never carry in a one-line field.
func controlSamples() map[string]string {
	return map[string]string{
		"NUL": "\x00", "ESC": "\x1b", "backspace": "\b", "vertical tab": "\v", "form feed": "\f",
		"tab": "\t", "DEL": "\x7f", "C1 NEL": "\u0085", "C1 CSI": "\u009b",
	}
}

func TestValidDoneWhen_Lines(t *testing.T) {
	if err := ValidDoneWhen(nil); err != nil {
		t.Errorf("nil: %v", err)
	}
	if err := ValidDoneWhen([]string{}); err != nil {
		t.Errorf("empty: %v", err)
	}
	ten := make([]string, 10)
	for i := range ten {
		ten[i] = fmt.Sprintf("line %d", i)
	}
	if err := ValidDoneWhen(ten); err != nil {
		t.Errorf("10 lines: %v", err)
	}
	if err := ValidDoneWhen(append(ten, "eleven")); err == nil {
		t.Error("11 lines: want an error")
	}
}

func TestValidDoneWhen_EachLine(t *testing.T) {
	if err := ValidDoneWhen([]string{strings.Repeat("字", 200)}); err != nil {
		t.Errorf("200 runes: %v", err)
	}
	bad := map[string][]string{
		"201 runes": {strings.Repeat("a", 201)},
		"empty":     {"ok", ""},
		"newline":   {"a\nb"},
		"cr":        {"a\rb"},
		"bad utf8":  {"\xff"},
	}
	for name, v := range bad {
		if err := ValidDoneWhen(v); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	for name, s := range controlSamples() {
		if err := ValidDoneWhen([]string{"fine", "a" + s + "b"}); err == nil {
			t.Errorf("done_when with %s: want an error", name)
		}
	}
	if err := ValidDoneWhen([]string{"ok\x1b[2Jspoof"}); err == nil {
		t.Error("an ANSI escape in a done_when line must be refused")
	}
}

func TestValidTaskDescription(t *testing.T) {
	if err := ValidTaskDescription(""); err != nil {
		t.Errorf("empty is allowed: %v", err)
	}
	if err := ValidTaskDescription("line one\nline two"); err != nil {
		t.Errorf("multi-line is allowed: %v", err)
	}
	if err := ValidTaskDescription(strings.Repeat("a", 32*1024)); err != nil {
		t.Errorf("32 KiB: %v", err)
	}
	if err := ValidTaskDescription(strings.Repeat("a", 32*1024+1)); err == nil {
		t.Error("32 KiB + 1: want an error")
	}
	if err := ValidTaskDescription("a\xffb"); err == nil {
		t.Error("invalid UTF-8: want an error")
	}
	// A description may carry line breaks and tabs; every other control
	// character is refused.
	if err := ValidTaskDescription("a\n\tb\r\nc"); err != nil {
		t.Errorf("newline, tab and CR are allowed: %v", err)
	}
	for name, s := range controlSamples() {
		if name == "tab" {
			continue
		}
		if err := ValidTaskDescription("a" + s + "b"); err == nil {
			t.Errorf("description with %s: want an error", name)
		}
	}
	if err := ValidTaskDescription("ok\x1b[2Jspoof"); err == nil {
		t.Error("an ANSI escape in a description must be refused")
	}
}

// The one transition table (decision D-T7): nothing leaves completed or
// deleted, a same-status move is refused, and only a lead closes a pending
// task or deletes one.
func TestTaskTransitionAllowed_Table(t *testing.T) {
	type k struct {
		from, to TaskStatus
		by       TaskActor
	}
	allowed := map[k]bool{
		{TaskPending, TaskInProgress, TaskByLead}:    true,
		{TaskPending, TaskInProgress, TaskByOwner}:   true,
		{TaskInProgress, TaskCompleted, TaskByLead}:  true,
		{TaskInProgress, TaskCompleted, TaskByOwner}: true,
		{TaskPending, TaskCompleted, TaskByLead}:     true,
		{TaskPending, TaskDeleted, TaskByLead}:       true,
		{TaskInProgress, TaskDeleted, TaskByLead}:    true,
	}
	all := []TaskStatus{TaskPending, TaskInProgress, TaskCompleted, TaskDeleted}
	for _, from := range all {
		for _, to := range all {
			for _, by := range []TaskActor{TaskByLead, TaskByOwner} {
				want := allowed[k{from, to, by}]
				if got := TaskTransitionAllowed(from, to, by); got != want {
					t.Errorf("TaskTransitionAllowed(%s, %s, %s) = %v, want %v", from, to, by, got, want)
				}
			}
		}
	}
	if TaskTransitionAllowed("bogus", TaskCompleted, TaskByLead) || TaskTransitionAllowed(TaskPending, "bogus", TaskByLead) {
		t.Error("an unknown status is never allowed")
	}
}

func TestTaskWire_OmitsEmptyOptionals(t *testing.T) {
	b, err := json.Marshal(Task{ID: "abcdef-1", TeamID: "t", Subject: "s", Status: TaskPending})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, key := range []string{`"last_report"`, `"last_turn"`, `"description"`, `"metadata"`} {
		if strings.Contains(s, key) {
			t.Errorf("%s should be omitted when empty: %s", key, s)
		}
	}
	if !strings.Contains(s, `"id":"abcdef-1"`) || !strings.Contains(s, `"status":"pending"`) {
		t.Errorf("unexpected wire: %s", s)
	}
}
