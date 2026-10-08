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

// ---- reports (T-1a2) ----

func TestValidReportKind(t *testing.T) {
	for _, k := range []ReportKind{ReportAck, ReportProgress, ReportQuestion, ReportReady, ReportMerged, ReportBlocked, ReportDone} {
		if !ValidReportKind(k) {
			t.Errorf("%q should be a kind", k)
		}
	}
	for _, k := range []ReportKind{"", "ACK", "finished", "ack "} {
		if ValidReportKind(k) {
			t.Errorf("%q should not be a kind", k)
		}
	}
}

func TestValidReportID(t *testing.T) {
	good := []string{"3f2a9c01-aaaa-4000-8000-000000000001", "0e8f3c5a-1b2d-4c6e-9f70-a1b2c3d4e5f6"}
	for _, id := range good {
		if err := ValidReportID(id); err != nil {
			t.Errorf("ValidReportID(%q) = %v, want nil", id, err)
		}
	}
	bad := []string{"", "abc", "3F2A9C01-AAAA-4000-8000-000000000001", " 3f2a9c01-aaaa-4000-8000-000000000001",
		"3f2a9c01aaaa40008000000000000001", "3f2a9c01-aaaa-4000-8000-00000000000g", "3f2a9c01-aaaa-4000-8000-0000000000012"}
	for _, id := range bad {
		if err := ValidReportID(id); err == nil {
			t.Errorf("ValidReportID(%q) = nil, want an error", id)
		}
	}
}

// goodReport is a valid request of kind k with just its required fields.
func goodReport(k ReportKind) ReportRequest {
	r := ReportRequest{Kind: k, Summary: "did a thing"}
	switch k {
	case ReportQuestion, ReportBlocked:
		r.Needs = "lead"
	case ReportReady:
		r.PR, r.Reviews = 12, []string{"R1=job-1"}
	case ReportMerged:
		r.PR, r.SHA = 12, "abcdef1"
	}
	return r
}

// errNames asserts err mentions field as its subject.
func errNames(t *testing.T, label string, err error, field string) {
	t.Helper()
	if err == nil {
		t.Errorf("%s: want an error naming %q", label, field)
		return
	}
	if !strings.HasPrefix(err.Error(), field) {
		t.Errorf("%s: error %q should start with the field %q", label, err, field)
	}
}

func tenReviews() []string {
	out := make([]string, 10)
	for i := range out {
		out[i] = fmt.Sprintf("R%d=j%d", i+1, i+1)
	}
	return out
}

func TestValidateReport_PerKindRequiredFields(t *testing.T) {
	kinds := []ReportKind{ReportAck, ReportProgress, ReportQuestion, ReportReady, ReportMerged, ReportBlocked, ReportDone}
	for _, k := range kinds {
		if err := ValidateReport(goodReport(k)); err != nil {
			t.Errorf("a minimal %s report: %v", k, err)
		}
	}

	t.Run("unknown kind", func(t *testing.T) {
		errNames(t, "empty kind", ValidateReport(ReportRequest{Summary: "s"}), "kind")
		errNames(t, "bogus kind", ValidateReport(ReportRequest{Kind: "finished", Summary: "s"}), "kind")
	})

	t.Run("missing or wrong required field", func(t *testing.T) {
		cases := []struct {
			name  string
			mut   func(*ReportRequest)
			kind  ReportKind
			field string
		}{
			{"question without needs", func(r *ReportRequest) { r.Needs = "" }, ReportQuestion, "needs"},
			{"question needs=everyone", func(r *ReportRequest) { r.Needs = "everyone" }, ReportQuestion, "needs"},
			{"question needs=Lead", func(r *ReportRequest) { r.Needs = "Lead" }, ReportQuestion, "needs"},
			{"blocked without needs", func(r *ReportRequest) { r.Needs = "" }, ReportBlocked, "needs"},
			{"blocked needs=nobody", func(r *ReportRequest) { r.Needs = "nobody" }, ReportBlocked, "needs"},
			{"ready without pr", func(r *ReportRequest) { r.PR = 0 }, ReportReady, "pr"},
			{"ready negative pr", func(r *ReportRequest) { r.PR = -3 }, ReportReady, "pr"},
			{"ready without reviews", func(r *ReportRequest) { r.Reviews = nil }, ReportReady, "reviews"},
			{"ready empty reviews", func(r *ReportRequest) { r.Reviews = []string{} }, ReportReady, "reviews"},
			{"merged without pr", func(r *ReportRequest) { r.PR = 0 }, ReportMerged, "pr"},
			{"merged without sha", func(r *ReportRequest) { r.SHA = "" }, ReportMerged, "sha"},
			{"merged sha 6 chars", func(r *ReportRequest) { r.SHA = "abcdef" }, ReportMerged, "sha"},
			{"merged sha 41 chars", func(r *ReportRequest) { r.SHA = strings.Repeat("a", 41) }, ReportMerged, "sha"},
			{"merged sha not hex", func(r *ReportRequest) { r.SHA = "abcdefg" }, ReportMerged, "sha"},
			{"merged sha with space", func(r *ReportRequest) { r.SHA = "abcdef1 " }, ReportMerged, "sha"},
		}
		for _, c := range cases {
			r := goodReport(c.kind)
			c.mut(&r)
			errNames(t, c.name, ValidateReport(r), c.field)
		}
	})

	t.Run("sha lengths and case", func(t *testing.T) {
		for _, sha := range []string{"abcdef1", strings.Repeat("a", 40), "ABCDEF1", "AbCdEf1234"} {
			r := goodReport(ReportMerged)
			r.SHA = sha
			if err := ValidateReport(r); err != nil {
				t.Errorf("sha %q: %v", sha, err)
			}
		}
	})

	t.Run("fields that do not belong to the kind", func(t *testing.T) {
		stray := []struct {
			field string
			set   func(*ReportRequest)
			ok    map[ReportKind]bool // kinds that accept it
		}{
			{"needs", func(r *ReportRequest) { r.Needs = "lead" }, map[ReportKind]bool{ReportQuestion: true, ReportBlocked: true}},
			{"pr", func(r *ReportRequest) { r.PR = 7 }, map[ReportKind]bool{ReportReady: true, ReportMerged: true}},
			{"reviews", func(r *ReportRequest) { r.Reviews = []string{"R1=j"} }, map[ReportKind]bool{ReportReady: true}},
			{"sha", func(r *ReportRequest) { r.SHA = "abcdef1" }, map[ReportKind]bool{ReportMerged: true}},
		}
		for _, k := range kinds {
			for _, s := range stray {
				if s.ok[k] {
					continue
				}
				r := goodReport(k)
				s.set(&r)
				errNames(t, fmt.Sprintf("%s with stray %s", k, s.field), ValidateReport(r), s.field)
			}
		}
	})

	t.Run("summary", func(t *testing.T) {
		for _, s := range []string{strings.Repeat("a", 200), strings.Repeat("字", 200), "x"} {
			r := goodReport(ReportProgress)
			r.Summary = s
			if err := ValidateReport(r); err != nil {
				t.Errorf("summary of %d runes: %v", len([]rune(s)), err)
			}
		}
		bad := map[string]string{
			"empty": "", "201 runes": strings.Repeat("a", 201), "201 CJK": strings.Repeat("字", 201),
			"newline": "a\nb", "ESC": "ok\x1b[2Jspoof", "bad utf8": "a\xffb",
		}
		for name, s := range bad {
			r := goodReport(ReportProgress)
			r.Summary = s
			errNames(t, "summary "+name, ValidateReport(r), "summary")
		}
		for name, c := range controlSamples() {
			r := goodReport(ReportProgress)
			r.Summary = "a" + c + "b"
			errNames(t, "summary with "+name, ValidateReport(r), "summary")
		}
	})

	t.Run("body", func(t *testing.T) {
		for _, k := range kinds { // every kind accepts a body
			r := goodReport(k)
			r.Body = "line one\n\tline two\r\n"
			if err := ValidateReport(r); err != nil {
				t.Errorf("%s with a body: %v", k, err)
			}
		}
		r := goodReport(ReportProgress)
		r.Body = strings.Repeat("a", 32*1024)
		if err := ValidateReport(r); err != nil {
			t.Errorf("32 KiB body: %v", err)
		}
		r.Body = strings.Repeat("a", 32*1024+1)
		errNames(t, "32 KiB + 1 body", ValidateReport(r), "body")
		r.Body = "a\xffb"
		errNames(t, "bad utf8 body", ValidateReport(r), "body")
		for name, c := range controlSamples() {
			if name == "tab" {
				continue
			}
			r.Body = "a" + c + "b"
			errNames(t, "body with "+name, ValidateReport(r), "body")
		}
	})

	t.Run("reviews entries", func(t *testing.T) {
		ok := [][]string{
			{"R1=job"}, {"R1=a=b"}, {"R1=j1", "R2=j2"}, {"R1=" + strings.Repeat("j", 197)}, tenReviews(),
		}
		for _, rv := range ok {
			r := goodReport(ReportReady)
			r.Reviews = rv
			if err := ValidateReport(r); err != nil {
				t.Errorf("reviews %v: %v", rv, err)
			}
		}
		bad := map[string][]string{
			"no equals":      {"R1"},
			"empty stage":    {"=job"},
			"empty job":      {"R1="},
			"space in stage": {"R 1=job"},
			"space in job":   {"R1=jo b"},
			"leading space":  {" R1=job"},
			"newline":        {"R1=job\n"},
			"tab":            {"R1=jo\tb"},
			"ESC":            {"R1=jo\x1bb"},
			"bad utf8":       {"R1=jo\xffb"},
			"201 runes":      {"R1=" + strings.Repeat("j", 198)},
			"11 entries":     append(tenReviews(), "R11=j"),
			"one bad of two": {"R1=j1", "R2"},
			"empty entry":    {""},
		}
		for name, rv := range bad {
			r := goodReport(ReportReady)
			r.Reviews = rv
			errNames(t, "reviews "+name, ValidateReport(r), "reviews")
		}
	})
}

func TestReportWire_Shape(t *testing.T) {
	b, err := json.Marshal(ReportRequest{Kind: ReportAck, Summary: "s"})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"id"`, `"task"`, `"needs"`, `"pr"`, `"reviews"`, `"sha"`, `"body"`} {
		if strings.Contains(string(b), key) {
			t.Errorf("%s should be omitted from a request when empty: %s", key, b)
		}
	}
	b, err = json.Marshal(Report{ID: "i", Task: "abcdef-1", Kind: ReportReady, Summary: "s", PR: 3, Reviews: []string{"R1=j"},
		Member: TaskOwner{Ref: "_a"}, CreatedAt: 9})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"id":"i"`, `"task":"abcdef-1"`, `"kind":"ready"`, `"pr":3`, `"reviews":["R1=j"]`, `"member":{"ref":"_a"}`, `"created_at":9`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("wire lacks %s: %s", want, b)
		}
	}
	for _, key := range []string{`"needs"`, `"sha"`, `"body"`} {
		if strings.Contains(string(b), key) {
			t.Errorf("%s should be omitted when empty: %s", key, b)
		}
	}
}
