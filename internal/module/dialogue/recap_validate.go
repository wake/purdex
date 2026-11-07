package dialogue

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/wake/purdex/internal/redact"
)

// Limits of a recap, in characters (runes).
const (
	maxRecapHeadline = 60
	maxRecapDone     = 6 // items
	maxRecapItem     = 80
	maxRecapNow      = 160
	maxRecapNext     = 160
)

// recapPrompt is the system prompt of a recap job (plan appendix B-2).
//
//go:embed prompts/recap-v2.txt
var recapPrompt string

// recapOut is the model's answer to a recap job, and what dlg_recaps.body_json stores.
type recapOut struct {
	Headline string   `json:"headline"`
	Done     []string `json:"done"`
	Now      string   `json:"now"`
	Decided  []string `json:"decided"`
	Waiting  []string `json:"waiting"`
	Next     string   `json:"next"`
}

// parseRecap reads the model's text as a recapOut: one JSON object (one code fence around it is allowed), the six fields
// all present and of the right type (three strings, three arrays of strings), no repeated or extra field, nothing before
// or after. Anything else is errFormat.
func parseRecap(text string) (recapOut, error) {
	if len(text) > maxModelJSON {
		return recapOut{}, formatErr("too long")
	}
	body := stripFence(strings.TrimSpace(text))
	if hasDuplicateMember(body) {
		return recapOut{}, formatErr("repeated field")
	}
	var members map[string]json.RawMessage
	if json.Unmarshal([]byte(body), &members) != nil {
		return recapOut{}, formatErr("not a JSON object")
	}
	for _, k := range []string{"headline", "done", "now", "decided", "waiting", "next"} {
		if _, ok := members[k]; !ok {
			return recapOut{}, formatErr("missing " + k)
		}
	}
	if len(members) != 6 {
		return recapOut{}, formatErr("extra field")
	}
	for _, k := range []string{"done", "decided", "waiting"} {
		if !strings.HasPrefix(strings.TrimSpace(string(members[k])), "[") { // null decodes into a nil slice without error
			return recapOut{}, formatErr(k + " is not an array")
		}
	}
	dec := json.NewDecoder(strings.NewReader(body))
	dec.DisallowUnknownFields()
	var o recapOut
	if err := dec.Decode(&o); err != nil || dec.More() {
		return recapOut{}, formatErr("wrong field type")
	}
	return o, nil
}

// repairRecap applies the limits in code: every string is redacted first; headline, now and next are cut at a word
// boundary; empty list items are dropped, then done keeps its first six; every list item is cut to 80.
func repairRecap(o recapOut) recapOut {
	clean := func(s string, n int) string { return cutAtWord(strings.TrimSpace(redact.String(s)), n) }
	items := func(in []string) []string {
		var out []string
		for _, s := range in {
			if s = clean(s, maxRecapItem); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	o.Headline = clean(o.Headline, maxRecapHeadline)
	o.Now = clean(o.Now, maxRecapNow)
	o.Next = clean(o.Next, maxRecapNext)
	o.Done = items(o.Done)
	if len(o.Done) > maxRecapDone {
		o.Done = o.Done[:maxRecapDone]
	}
	o.Decided = items(o.Decided)
	o.Waiting = items(o.Waiting)
	return o
}

func localHM(ms int64) string { return time.UnixMilli(ms).Format("15:04") }

// recapText is a recap as it stands in the dialogue file: "<recap HH:MM | turns a–b>" and the structured body, or with
// carried the same text under "<carried recap …>" (the first line of a new segment). Empty lists and empty now/next are left out.
func recapText(r RecapRow, o recapOut, carried bool) string {
	tag := "recap"
	if carried {
		tag = "carried recap"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<%s %s | turns %d–%d>\n", tag, localHM(r.At), r.FromNo, r.ToNo)
	fmt.Fprintf(&b, "headline: %s\n", oneLine(o.Headline))
	list := func(name string, in []string) {
		if len(in) == 0 {
			return
		}
		b.WriteString(name + ":\n")
		for _, s := range in {
			b.WriteString("- " + oneLine(s) + "\n")
		}
	}
	line := func(name, s string) {
		if s = oneLine(s); s != "" {
			b.WriteString(name + ": " + s + "\n")
		}
	}
	list("done", o.Done)
	line("now", o.Now)
	list("decided", o.Decided)
	list("waiting", o.Waiting)
	line("next", o.Next)
	return b.String()
}
