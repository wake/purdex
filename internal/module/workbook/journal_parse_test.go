package workbook

import (
	"errors"
	"strings"
	"testing"
)

const okJournal = `{"events":[{"kind":"request","by":"user","from":"使用者","to":"88","text":"使用者要求查清楚。"},` +
	`{"kind":"comm","from":"1f","to":"88","text":"1f 通知已部署。"},` +
	`{"kind":"confirm","from":"88","to":"使用者","text":"還有兩件事。","items":["甲","乙"],"detail_start":"要你決定"}],` +
	`"confirm_done":[7,8],"roadmap":[{"feature":"工作簿日誌","stage":"J-1","status":"進行中"}]}`

func TestParseJournalJSON_Valid(t *testing.T) {
	out, err := ParseJournalJSON(okJournal)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Events) != 3 || out.Events[0].By != "user" || out.Events[2].Items[1] != "乙" || out.Events[2].DetailStart != "要你決定" {
		t.Fatalf("events = %+v", out.Events)
	}
	if len(out.ConfirmDone) != 2 || out.ConfirmDone[1] != 8 || len(out.Roadmap) != 1 || out.Roadmap[0].Stage != "J-1" || out.Dropped != 0 {
		t.Fatalf("out = %+v", out)
	}
	if _, err := ParseJournalJSON("```json\n" + okJournal + "\n```"); err != nil {
		t.Errorf("one fence is allowed: %v", err)
	}
	if o, err := ParseJournalJSON(`{"events":[],"confirm_done":[],"roadmap":[]}`); err != nil || len(o.Events)+len(o.ConfirmDone)+len(o.Roadmap) != 0 {
		t.Errorf("empty arrays: %+v %v", o, err)
	}
}

// Every malformed shape is a format failure, none of them a zero-value event.
// Mutation gate: replace the checks with a lenient json.Unmarshal into the structs -> a null item / a non-object /
// a wrong type decodes to a zero value and these cases stop failing (red).
func TestParseJournalJSON_MalformedShapes(t *testing.T) {
	cases := map[string]string{
		"not json":              `hello`,
		"two fences":            "```json\n{}\n```\n```json\n{}\n```",
		"top-level array":       `[]`,
		"missing events":        `{"confirm_done":[],"roadmap":[]}`,
		"missing confirm_done":  `{"events":[],"roadmap":[]}`,
		"missing roadmap":       `{"events":[],"confirm_done":[]}`,
		"events null":           `{"events":null,"confirm_done":[],"roadmap":[]}`,
		"events object":         `{"events":{},"confirm_done":[],"roadmap":[]}`,
		"confirm_done string":   `{"events":[],"confirm_done":"1","roadmap":[]}`,
		"roadmap null":          `{"events":[],"confirm_done":[],"roadmap":null}`,
		"null event":            `{"events":[null],"confirm_done":[],"roadmap":[]}`,
		"string event":          `{"events":["comm"],"confirm_done":[],"roadmap":[]}`,
		"array event":           `{"events":[[]],"confirm_done":[],"roadmap":[]}`,
		"text is a number":      `{"events":[{"kind":"comm","text":5}],"confirm_done":[],"roadmap":[]}`,
		"items is a string":     `{"events":[{"kind":"confirm","text":"x","items":"a"}],"confirm_done":[],"roadmap":[]}`,
		"items null element":    `{"events":[{"kind":"confirm","text":"x","items":["a",null]}],"confirm_done":[],"roadmap":[]}`,
		"items number element":  `{"events":[{"kind":"confirm","text":"x","items":["a",3]}],"confirm_done":[],"roadmap":[]}`,
		"duplicate key in item": `{"events":[{"kind":"comm","text":"a","text":"b"}],"confirm_done":[],"roadmap":[]}`,
		"duplicate top key":     `{"events":[],"events":[],"confirm_done":[],"roadmap":[]}`,
		"confirm_done null":     `{"events":[],"confirm_done":[null],"roadmap":[]}`,
		"confirm_done float":    `{"events":[],"confirm_done":[1.5],"roadmap":[]}`,
		"confirm_done string n": `{"events":[],"confirm_done":["3"],"roadmap":[]}`,
		"roadmap null item":     `{"events":[],"confirm_done":[],"roadmap":[null]}`,
		"roadmap string item":   `{"events":[],"confirm_done":[],"roadmap":["x"]}`,
		"roadmap dup key":       `{"events":[],"confirm_done":[],"roadmap":[{"feature":"a","feature":"b","stage":"s","status":"完成"}]}`,
		"roadmap number field":  `{"events":[],"confirm_done":[],"roadmap":[{"feature":"a","stage":2,"status":"完成"}]}`,
		"text null":             `{"events":[{"kind":"comm","text":null}],"confirm_done":[],"roadmap":[]}`,
		"from null":             `{"events":[{"kind":"comm","from":null,"text":"x"}],"confirm_done":[],"roadmap":[]}`,
		"items null":            `{"events":[{"kind":"confirm","text":"x","items":null}],"confirm_done":[],"roadmap":[]}`,
		"detail_start null":     `{"events":[{"kind":"confirm","text":"x","items":["a"],"detail_start":null}],"confirm_done":[],"roadmap":[]}`,
		"roadmap stage null":    `{"events":[],"confirm_done":[],"roadmap":[{"feature":"a","stage":null,"status":"完成"}]}`,
		"trailing text":         okJournal + ` trailing`,
	}
	for name, text := range cases {
		if _, err := ParseJournalJSON(text); !errors.Is(err, ErrFormat) {
			t.Errorf("%s: err = %v, want ErrFormat", name, err)
		}
	}
}

// Items that are well-formed but unusable are dropped and counted, the rest kept.
func TestParseJournalJSON_DropsUnusableItems(t *testing.T) {
	out, err := ParseJournalJSON(`{"events":[` +
		`{"kind":"task_new","text":"#3 做事"},` + // a task kind is never the model's
		`{"kind":"nonsense","text":"x"},` +
		`{"kind":"comm","text":"  "},` + // no text
		`{"kind":"request","text":"x"},` + // a request needs by
		`{"kind":"request","by":"boss","text":"x"},` +
		`{"kind":"comm","text":"留下"}],` +
		`"confirm_done":[],"roadmap":[` +
		`{"feature":"f","stage":"s","status":""},{"feature":"","stage":"s","status":"完成"},{"feature":"f","stage":"s","status":"完成"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Events) != 1 || out.Events[0].Text != "留下" || len(out.Roadmap) != 1 || out.Dropped != 7 {
		t.Fatalf("out = %+v", out)
	}
}

// The v2 parser still refuses a journal answer and the journal parser a v2 answer: the two jobs never mix.
func TestParseJournalJSON_IsNotTheV2Parser(t *testing.T) {
	if _, err := ParseModelJSON(okJournal); !errors.Is(err, ErrFormat) {
		t.Error("v2 parser accepted a journal answer")
	}
	v2 := `{"skip":false,"thing":"a","push":"b","entry":"c","status":"d","thing_done":false,"todos":{"done":[],"dropped":[],"add":[]}}`
	if _, err := ParseJournalJSON(v2); !errors.Is(err, ErrFormat) {
		t.Error("journal parser accepted a v2 answer")
	}
}

// A turn leaves at most MaxJournalEvents events; the rest is dropped and counted (the prompt says 0 to 4).
func TestParseJournalJSON_CapsEvents(t *testing.T) {
	ev := `{"kind":"comm","text":"通知"}`
	for _, c := range []struct{ n, kept, dropped int }{{4, 4, 0}, {5, 4, 1}, {500, 4, 496}} {
		items := make([]string, c.n)
		for i := range items {
			items[i] = ev
		}
		out, err := ParseJournalJSON(`{"events":[` + strings.Join(items, ",") + `],"confirm_done":[],"roadmap":[]}`)
		if err != nil || len(out.Events) != c.kept || out.Dropped != c.dropped {
			t.Errorf("%d events: kept %d dropped %d err %v, want %d / %d", c.n, len(out.Events), out.Dropped, err, c.kept, c.dropped)
		}
	}
}
