package team

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

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
	good := []string{"3f2a9c01-aaaa-4000-8000-000000000001", "0e8f3c5a-1b2d-4c6e-9f70-a1b2c3d4e5f6",
		"0e8f3c5a-1b2d-4c6e-a000-a1b2c3d4e5f6", "0e8f3c5a-1b2d-4c6e-bfff-a1b2c3d4e5f6", "0e8f3c5a-1b2d-4c6e-8fff-a1b2c3d4e5f6"}
	for _, id := range good {
		if err := ValidReportID(id); err != nil {
			t.Errorf("ValidReportID(%q) = %v, want nil", id, err)
		}
	}
	bad := []string{"", "abc",
		"00000000-0000-0000-0000-000000000000",   // all zero: no version, no variant
		"3f2a9c01-aaaa-1000-8000-000000000001",   // version 1
		"3f2a9c01-aaaa-5000-8000-000000000001",   // version 5
		"3f2a9c01-aaaa-4000-0000-000000000001",   // variant 0 (NCS)
		"3f2a9c01-aaaa-4000-c000-000000000001",   // variant c (Microsoft)
		"3f2a9c01-aaaa-4000-f000-000000000001",   // variant f (reserved)
		"3f2a9c01-aaaa-4000-8000-00000000000a\n", // trailing newline
		"ffffffff-ffff-ffff-ffff-ffffffffffff",   // all ones
		"3F2A9C01-AAAA-4000-8000-000000000001", " 3f2a9c01-aaaa-4000-8000-000000000001",
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
			{"R1=job"}, {"R1=j1", "R2=j2"}, {"R1=" + strings.Repeat("j", 197)}, tenReviews(),
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
			"two equals":     {"R1=a=b"},
			"equals at end":  {"R1=job="},
			"only equals":    {"="},
			"double equals":  {"R1==job"},
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
