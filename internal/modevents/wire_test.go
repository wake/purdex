package modevents

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

const (
	testStream = "Ab3_-xyz09"
	testSID    = "0f8e2c1a-1b2c-4d3e-8f90-a1b2c3d4e5f6"
)

// batchJSON builds a batch body; events is the raw JSON array.
func batchJSON(v int, stream, events string) string {
	return fmt.Sprintf(`{"v":%d,"stream":%q,"agent":"cc","cc_version":"2.1.293","mod_version":"1.0.0-alpha.596","dropped_total":0,"events":%s}`, v, stream, events)
}

func ev(seq int64, sid, typ string) string {
	return fmt.Sprintf(`{"seq":%d,"at":1791409762960,"sid":%q,"type":%q,"data":{}}`, seq, sid, typ)
}

// evData builds one event whose data member is the raw dataField, e.g.
// `,"data":null`; an empty dataField leaves data out.
func evData(typ, dataField string) string {
	return fmt.Sprintf(`{"seq":1,"at":1791409762960,"sid":%q,"type":%q%s}`, testSID, typ, dataField)
}

func evs(items ...string) string { return "[" + strings.Join(items, ",") + "]" }

func TestDecodeBatch_Codes(t *testing.T) {
	many := make([]string, 501)
	for i := range many {
		many[i] = ev(int64(i+1), testSID, "heartbeat")
	}
	valid := batchJSON(1, testStream, evs(ev(1, testSID, "turn.start"), ev(2, testSID, "turn.complete")))

	cases := []struct {
		name       string
		body       string
		code       string // "" = valid
		withStream bool   // WireError.Stream must be testStream (else empty)
	}{
		{"valid", valid, "", false},
		{"not json", `{"v":1,`, CodeBadJSON, false},
		{"wrong shape", `[1,2]`, CodeBadJSON, false},
		{"wrong field type", `{"v":"1","stream":"` + testStream + `","events":[]}`, CodeBadJSON, false},
		{"trailing object", valid + `{"v":1}`, CodeBadJSON, true},
		{"trailing junk", valid + `x`, CodeBadJSON, true},
		{"trailing junk, bad stream", batchJSON(1, "short", evs(ev(1, testSID, "turn.start"))) + `x`, CodeBadJSON, false},
		{"malformed first object", `{"v":1,"stream":"` + testStream + `","events":[}`, CodeBadJSON, false},
		{"unsupported version", batchJSON(2, testStream, evs(ev(1, testSID, "turn.start"))), CodeUnsupportedVersion, true},
		{"unsupported version, bad stream", batchJSON(2, "short", evs(ev(1, testSID, "turn.start"))), CodeUnsupportedVersion, false},
		{"stream too short", batchJSON(1, "short", evs(ev(1, testSID, "turn.start"))), CodeBadStream, false},
		{"stream bad char", batchJSON(1, "abc def ghi", evs(ev(1, testSID, "turn.start"))), CodeBadStream, false},
		{"stream too long", batchJSON(1, strings.Repeat("a", 65), evs(ev(1, testSID, "turn.start"))), CodeBadStream, false},
		{"no events", batchJSON(1, testStream, `[]`), CodeBadEvents, true},
		{"null events", batchJSON(1, testStream, `null`), CodeBadEvents, true},
		{"501 events", batchJSON(1, testStream, evs(many...)), CodeBadEvents, true},
		{"negative dropped_total", strings.Replace(batchJSON(1, testStream, evs(ev(1, testSID, "turn.start"))), `"dropped_total":0`, `"dropped_total":-1`, 1), CodeBadEvents, true},
		{"equal seqs", batchJSON(1, testStream, evs(ev(3, testSID, "turn.start"), ev(3, testSID, "turn.complete"))), CodeBadSeq, true},
		{"decreasing seqs", batchJSON(1, testStream, evs(ev(3, testSID, "turn.start"), ev(2, testSID, "turn.complete"))), CodeBadSeq, true},
		{"zero seq", batchJSON(1, testStream, evs(ev(0, testSID, "turn.start"))), CodeBadSeq, true},
		{"negative seq", batchJSON(1, testStream, evs(ev(-1, testSID, "turn.start"))), CodeBadSeq, true},
		{"uppercase sid", batchJSON(1, testStream, evs(ev(1, strings.ToUpper(testSID), "turn.start"))), CodeBadSID, true},
		{"empty sid", batchJSON(1, testStream, evs(ev(1, "", "turn.start"))), CodeBadSID, true},
		{"not a uuid", batchJSON(1, testStream, evs(ev(1, "0f8e2c1a1b2c4d3e8f90a1b2c3d4e5f6", "turn.start"))), CodeBadSID, true},
		{"bad sid before bad event", batchJSON(1, testStream, evs(ev(1, "", "Turn.start"))), CodeBadSID, true},
		{"empty type", batchJSON(1, testStream, evs(ev(1, testSID, ""))), CodeBadEvent, true},
		{"uppercase type", batchJSON(1, testStream, evs(ev(1, testSID, "Turn.start"))), CodeBadEvent, true},
		{"65-char type", batchJSON(1, testStream, evs(ev(1, testSID, strings.Repeat("a", 65)))), CodeBadEvent, true},
		{"data missing", batchJSON(1, testStream, evs(evData("turn.start", ""))), CodeBadEvent, true},
		{"data null", batchJSON(1, testStream, evs(evData("turn.start", `,"data":null`))), CodeBadEvent, true},
		{"data array", batchJSON(1, testStream, evs(evData("turn.start", `,"data":[]`))), CodeBadEvent, true},
		{"data string", batchJSON(1, testStream, evs(evData("turn.start", `,"data":"x"`))), CodeBadEvent, true},
		{"data number", batchJSON(1, testStream, evs(evData("turn.start", `,"data":1`))), CodeBadEvent, true},
		{"data bool", batchJSON(1, testStream, evs(evData("turn.start", `,"data":true`))), CodeBadEvent, true},
		{"session.start with empty data object", batchJSON(1, testStream, evs(evData("session.start", `,"data":{}`))), "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := DecodeBatch(strings.NewReader(tc.body))
			if tc.code == "" {
				if err != nil {
					t.Fatalf("valid batch: %v", err)
				}
				if tc.name != "valid" {
					return
				}
				if b.V != 1 || b.Stream != testStream || b.Agent != "cc" || b.CCVersion != "2.1.293" || b.ModVersion != "1.0.0-alpha.596" || len(b.Events) != 2 {
					t.Fatalf("decoded = %+v", b)
				}
				if e := b.Events[0]; e.Seq != 1 || e.At != 1791409762960 || e.SID != testSID || e.Type != "turn.start" || string(e.Data) != "{}" {
					t.Fatalf("event = %+v", e)
				}
				return
			}
			var we *WireError
			if !errors.As(err, &we) {
				t.Fatalf("err = %v (%T), want *WireError %s", err, err, tc.code)
			}
			if we.Code != tc.code {
				t.Fatalf("code = %q, want %q", we.Code, tc.code)
			}
			want := ""
			if tc.withStream {
				want = testStream
			}
			if we.Stream != want {
				t.Fatalf("WireError.Stream = %q, want %q", we.Stream, want)
			}
		})
	}
}

func TestDecodeBatch_IgnoresUnknownFields(t *testing.T) {
	body := `{"v":1,"stream":"` + testStream + `","agent":"cc","future":{"x":1},"dropped_total":7,` +
		`"events":[{"seq":4,"at":5,"sid":"` + testSID + `","type":"some.new.type","data":{"k":"v"},"extra":true}]}`
	b, err := DecodeBatch(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if b.DroppedTotal != 7 || len(b.Events) != 1 || b.Events[0].Seq != 4 || b.Events[0].Type != "some.new.type" || string(b.Events[0].Data) != `{"k":"v"}` {
		t.Fatalf("decoded = %+v", b)
	}
}

// An older mod sends no cwd and no interactive: the batch decodes with
// their zero values. A U1-2a-1 mod sends both on every batch.
func TestDecodeBatch_EnvelopeFieldsOptional(t *testing.T) {
	old, err := DecodeBatch(strings.NewReader(batchJSON(1, testStream, evs(ev(1, testSID, "heartbeat")))))
	if err != nil {
		t.Fatal(err)
	}
	if old.CWD != "" || old.Interactive {
		t.Fatalf("an envelope without cwd / interactive decoded as %q / %v", old.CWD, old.Interactive)
	}
	body := `{"v":1,"stream":"` + testStream + `","agent":"cc","cwd":"/work/repo","interactive":true,"events":` + evs(ev(1, testSID, "heartbeat")) + `}`
	b, err := DecodeBatch(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if b.CWD != "/work/repo" || !b.Interactive {
		t.Fatalf("decoded cwd / interactive = %q / %v", b.CWD, b.Interactive)
	}
}

func TestKnownTypes(t *testing.T) {
	want := []string{"session.start", "session.switch", "session.end", "turn.start", "turn.complete",
		"tool.check", "tool.start", "tool.end", "agent.spawn", "compact.start", "compact.end",
		"usage", "background", "heartbeat"}
	got := KnownTypes()
	if len(got) != len(want) {
		t.Fatalf("KnownTypes() = %v", got)
	}
	for _, typ := range want {
		if !IsKnownType(typ) {
			t.Errorf("%s must be known", typ)
		}
	}
	if IsKnownType("turn.step") || IsKnownType("session.clear") || IsKnownType("") {
		t.Fatal("v1 does not know turn.step, session.clear or the empty type")
	}
}
