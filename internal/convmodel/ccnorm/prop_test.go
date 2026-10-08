package ccnorm

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
)

// genTranscript is a deterministic synthetic transcript of about n lines
// that exercises every row shape the normalizer reads: turns of every source,
// thinking / text / tool rows, queued prompts and attachments, interrupts,
// refusals, API errors, compaction, local commands, bash mode, titles,
// entrypoint and model changes, sidechain rows, unknown types and bad lines.
func genTranscript(seed uint64, n int) [][]byte {
	r := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	var lines [][]byte
	count := 0
	id := func(p string) string { count++; return fmt.Sprintf("%s-%d", p, count) }
	sec := 0.0
	tick := func() float64 { sec += 0.05 + r.Float64(); return sec }
	models := []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5"}
	model := models[0]
	entry := "cli"
	add := func(b []byte) { lines = append(lines, b) }

	for len(lines) < n {
		if r.IntN(12) == 0 {
			model = models[r.IntN(len(models))]
		}
		if r.IntN(25) == 0 {
			entry = map[string]string{"cli": "sdk-cli", "sdk-cli": "cli"}[entry]
		}
		ep := entrypoint(entry)
		switch r.IntN(20) {
		case 0:
			add([]byte("not json at all"))
		case 1:
			add(line(obj{"type": "last-prompt", "lastPrompt": "x", "sessionId": sidA}))
			add(aiTitle(fmt.Sprintf("title %d", count)))
		case 2:
			add(customTitle(fmt.Sprintf("name %d", count)))
		case 3:
			add(userRow(id("sc"), tick(), "subagent brief", sidechain()))
			add(assistantText(id("sc"), tick(), "sub", sidechain()))
		case 4:
			add(localCommandRow(id("lc"), tick(), "<command-name>/model</command-name>\n<command-args></command-args>"))
			add(localCommandRow(id("lc"), tick(), "<local-command-stdout>Set model</local-command-stdout>"))
		case 5:
			add(compactBoundary(id("cb"), tick(), []string{"auto", "manual"}[r.IntN(2)]))
			add(compactSummaryRow(id("cs"), tick()))
		case 6:
			add(line(obj{"type": "brand-new-type", "uuid": id("x")}))
		default:
			// a turn
			prompt := id("u")
			switch r.IntN(14) {
			case 0:
				add(oldUserRow(prompt, tick(), "old shape prompt", ep))
			case 1:
				add(userRow(prompt, tick(), "queued prompt", promptSource("queued"), ep))
			case 2:
				add(userRow(prompt, tick(), "<command-name>/relay</command-name><command-args>x</command-args>", ep))
			case 3:
				add(userRow(prompt, tick(), "<bash-input>ls</bash-input>", without("turnPosition"), ep))
				add(userRow(id("bo"), tick(), "<bash-stdout>a\nb</bash-stdout><bash-stderr></bash-stderr>", ep))
			case 4:
				add(userRow(prompt, tick(), peerText("hello", "peer-1"), isMeta(), originKind("peer"), turnOrigin("peer"), promptSource("system"), ep))
			case 5:
				add(userRow(prompt, tick(), taskNotification("done"), originKind("task-notification"), turnOrigin("task_notification"), promptSource("system"), ep))
			case 6:
				add(userRow(prompt, tick(), "wake up", isMeta(), without("origin"), turnOrigin("scheduled"), promptSource("system"), ep))
			case 7:
				add(userRow(prompt, tick(), "", blocksOf(obj{"type": "text", "text": "see [Image #1]"},
					obj{"type": "image", "source": obj{"type": "base64", "media_type": "image/png", "data": "AAAA"}}), ep))
			case 8:
				add(userRow(prompt, tick(), "automated", originKind("coordinator"), promptSource("system"), ep))
			default:
				add(userRow(prompt, tick(), fmt.Sprintf("prompt %d", count), ep))
			}
			for k := r.IntN(5); k > 0; k-- {
				switch r.IntN(8) {
				case 0:
					add(assistantThinking(id("t"), tick(), "", 100+r.IntN(900), ep))
				case 1:
					add(assistantThinking(id("t"), tick(), "pondering", 0, ep))
				case 2:
					tu := id("toolu")
					add(assistantRow(id("a"), tick(), model, toolUseBlock(tu, "Bash", obj{"command": "ls"}), ep))
					add(toolResultRow(id("r"), tick(), tu, "out", ep))
				case 3:
					add(queueOp(tick(), "enqueue"))
					add(queuedCommand(id("q"), tick(), "also this", obj{"kind": "human"}, "prompt"))
				case 4:
					add(multiBlockAssistant(id("m"), tick(), thinkingBlock("hm"), textBlock("one"), textBlock("two")))
				default:
					add(assistantRow(id("a"), tick(), model, textBlock(fmt.Sprintf("reply %d", count)), ep))
				}
			}
			switch r.IntN(10) {
			case 0:
				add(interruptRow(id("i"), tick(), r.IntN(2) == 0))
			case 1:
				add(interruptRow(id("i"), tick(), true))
				add(turnDuration(id("d"), tick(), 100, ep))
			case 2:
				add(apiErrorRow(id("e"), tick(), "rate_limit", "limit"))
				add(turnDuration(id("d"), tick(), 100, ep))
			case 3: // no end marker: a killed process or still running
			default:
				add(stopHookSummary(id("h"), tick()))
				add(turnDuration(id("d"), tick(), 1000, ep))
			}
		}
	}
	return lines
}

// run feeds lines straight and returns the normalizer, the snapshot (as JSON)
// after each prefix length in snaps, and all changes.
func run(t testing.TB, lines [][]byte, snaps map[int]bool) (*Normalizer, map[int]string, []Change) {
	t.Helper()
	n := New(Options{SessionID: sidA})
	snap := map[int]string{}
	var all []Change
	for i, l := range lines {
		ch, err := n.Feed(n.Next(), l)
		if err != nil {
			t.Fatalf("line %d: %v", i, err)
		}
		all = append(all, ch...)
		if snaps[i+1] {
			snap[i+1] = jsonOf(t, validated(t, n))
		}
	}
	return n, snap, all
}

func checkInvariance(t *testing.T, lines [][]byte, seed uint64) {
	t.Helper()
	snapAt := map[int]bool{}
	for k := 1; k <= len(lines); k += 1 + len(lines)/40 {
		snapAt[k] = true
	}
	ref, snaps, changes := run(t, lines, snapAt)
	want := jsonOf(t, validated(t, ref))
	wantStats := ref.Stats()

	// a feed in random whole-line chunks, each chunk preceded by an
	// overlapping re-read of the lines just before it (what overlapping
	// `after` reads of the transcript API give), with reads and a refused gap
	// in between
	r := rand.New(rand.NewPCG(seed, 7))
	offs := make([]int64, len(lines))
	var o int64
	for i, l := range lines {
		offs[i] = o
		o += int64(len(l)) + 1
	}
	n := New(Options{SessionID: sidA})
	for i := 0; i < len(lines); {
		back := min(i, r.IntN(5))
		for j := i - back; j < i; j++ {
			if ch, err := n.Feed(offs[j], lines[j]); err != nil || len(ch) != 0 {
				t.Fatalf("overlap re-read of line %d: changes %v err %v", j, ch, err)
			}
		}
		size := 1 + r.IntN(9)
		for k := 0; k < size && i < len(lines); k++ {
			if _, err := n.Feed(offs[i], lines[i]); err != nil {
				t.Fatalf("line %d: %v", i, err)
			}
			i++
		}
		wantNext := o
		if i < len(lines) {
			wantNext = offs[i]
		}
		if n.Next() != wantNext {
			t.Fatalf("Next = %d after %d lines, want %d", n.Next(), i, wantNext)
		}
		_ = n.Conversation()
		_ = n.Stats()
		n.Position("x", "")
		if r.IntN(4) == 0 {
			if _, err := n.Feed(n.Next()+1+int64(r.IntN(50)), lines[0]); !errors.Is(err, ErrGap) {
				t.Fatalf("gap not refused: %v", err)
			}
		}
	}
	if got := jsonOf(t, validated(t, n)); got != want {
		t.Fatalf("chunked feed differs from the straight feed (seed %d)", seed)
	}
	st := n.Stats()
	if st.Lines != wantStats.Lines || st.BadJSON != wantStats.BadJSON || fmt.Sprint(st.Skipped) != fmt.Sprint(wantStats.Skipped) {
		t.Errorf("stats differ: %+v vs %+v", st, wantStats)
	}

	// every prefix snapshot equals a fresh normalization of that prefix
	for k, s := range snaps {
		fresh, _, _ := run(t, lines[:k], nil)
		if got := jsonOf(t, validated(t, fresh)); got != s {
			t.Fatalf("snapshot after %d lines differs from a fresh normalization of them", k)
		}
	}

	// SetLive(false) then (true) comes back to the live model
	ref.SetLive(false)
	validated(t, ref)
	ref.SetLive(true)
	if got := jsonOf(t, validated(t, ref)); got != want {
		t.Error("SetLive(false) then SetLive(true) did not restore the live model")
	}

	// the change list is coherent with the final model: it names only turns
	// and items that exist, at offsets inside the file, and names all of them
	known := map[string]bool{}
	for _, ch := range changes {
		if ch.Offset < 0 || ch.Offset >= ref.Next() {
			t.Fatalf("change %+v has an offset outside the file", ch)
		}
		if _, ok := ref.Position(ch.TurnID, ch.ItemID); !ok {
			t.Fatalf("change %+v names something that does not exist", ch)
		}
		known[ch.TurnID+"\x00"+ch.ItemID] = true
	}
	for _, tr := range ref.Conversation().Turns {
		if !known[tr.ID+"\x00"] {
			t.Errorf("turn %q was never reported", tr.ID)
		}
		for _, it := range tr.Items {
			if !known[tr.ID+"\x00"+itemID(it)] {
				t.Errorf("item %q was never reported", itemID(it))
			}
		}
	}
}

func TestFeed_ChunkInvariance(t *testing.T) {
	for seed := uint64(1); seed <= 3; seed++ {
		t.Run(fmt.Sprintf("generated-%d", seed), func(t *testing.T) {
			checkInvariance(t, genTranscript(seed, 2000), seed)
		})
	}
	if dir := os.Getenv("PDX_CC_SAMPLES"); dir != "" {
		files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
		for _, f := range files {
			t.Run(filepath.Base(f), func(t *testing.T) {
				checkInvariance(t, readLines(t, f), 99)
			})
		}
	}
}

func readLines(t *testing.T, path string) [][]byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out [][]byte
	rd := bufio.NewReaderSize(f, 1<<20)
	for {
		b, err := rd.ReadBytes('\n')
		if len(b) > 0 {
			out = append(out, bytes.TrimSuffix(b, []byte("\n")))
		}
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

// The generated transcript must itself cover the shapes it claims to, or the
// invariance test proves little.
func TestGen_CoversTheRowShapes(t *testing.T) {
	n, _, _ := run(t, genTranscript(1, 2000), nil)
	c := validated(t, n)
	seen := map[string]bool{}
	for _, tr := range c.Turns {
		seen["outcome:"+string(tr.Outcome)] = true
		for _, it := range tr.Items {
			seen[string(it.Type)] = true
			if it.User != nil {
				seen["source:"+string(it.User.Source)] = true
			}
			if it.System != nil {
				seen["system:"+string(it.System.Kind)] = true
			}
		}
	}
	for _, want := range []string{
		"outcome:done", "outcome:interrupted", "outcome:failed",
		"user", "agent_text", "thinking",
		"source:user", "source:queued", "source:slash", "source:bash", "source:peer", "source:task", "source:scheduled",
		"system:interrupted", "system:compacted", "system:command_output", "system:handoff", "system:model_changed",
	} {
		if !seen[want] {
			t.Errorf("generated transcript never produced %s", want)
		}
	}
	if c.Title == "" || c.Usage == nil {
		t.Errorf("title %q usage %+v", c.Title, c.Usage)
	}
	if st := n.Stats(); st.BadJSON == 0 || st.Skipped["sidechain"] == 0 {
		t.Errorf("stats = %+v", st)
	}
}

// FuzzFeed: Feed never panics on any bytes, and whatever it produced is a
// well-formed model (Validate passes) that marshals.
func FuzzFeed(f *testing.F) {
	join := func(ls ...[]byte) []byte { return bytes.Join(ls, []byte("\n")) }
	f.Add(join(userRow("u1", 1, "hi"), assistantText("a1", 2, "yo"), turnDuration("d1", 3, 5)))
	f.Add(join(userRow("u1", 1, "go"), assistantThinking("t1", 2, "", 9), interruptRow("i1", 3, false), userRow("u2", 4, "q", promptSource("queued"))))
	f.Add(join(apiErrorRow("e1", 1, "rate_limit", "limit"), compactBoundary("c1", 2, "auto"), localCommandRow("l1", 3, "<command-name>/x</command-name>")))
	f.Add(join(userRow("p1", 1, peerText("b", "n"), isMeta(), originKind("peer")), queuedCommand("q1", 2, "x", obj{"kind": "human"}, "prompt")))
	f.Add(join(userRow("b1", 1, "<bash-input>ls</bash-input>", without("turnPosition")), userRow("b2", 2, "<bash-stdout>x</bash-stdout>")))
	f.Add(join(userRow("u1", 1, "", blocksOf(obj{"type": "image", "source": obj{"type": "base64", "media_type": "image/png", "data": "AAAA"}}))))
	f.Add(join([]byte(`{"type":"user"`), []byte(`[]`), []byte(``), []byte(`{"type":"assistant","uuid":"x","message":{"content":7}}`)))
	f.Fuzz(func(t *testing.T, data []byte) {
		n := New(Options{})
		for _, l := range bytes.Split(data, []byte("\n")) {
			if _, err := n.Feed(n.Next(), l); err != nil {
				t.Fatalf("Feed: %v", err)
			}
		}
		check := func(stage string) {
			c := n.Conversation()
			if err := c.Validate(); err != nil {
				t.Fatalf("%s: Validate: %v\n%s", stage, err, dump(c))
			}
			if _, err := json.Marshal(c); err != nil {
				t.Fatalf("%s: marshal: %v", stage, err)
			}
		}
		check("live")
		n.SetLive(false)
		check("closed")
	})
}

// Rows shuffled, duplicated, dropped or cut at random bytes reach states a
// real transcript never does (a result before its call, the same uuid twice,
// a turn_duration first); the model must still validate.
func TestFeed_CorruptedTranscriptsStayWellFormed(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 6))
	base := genTranscript(11, 300)
	for iter := 0; iter < 300; iter++ {
		lines := make([][]byte, 0, len(base))
		for _, l := range base {
			switch r.IntN(12) {
			case 0: // drop
			case 1: // duplicate, at a new offset
				lines = append(lines, l, l)
			case 2: // cut
				lines = append(lines, l[:r.IntN(len(l)+1)])
			case 3: // flip a byte
				c := append([]byte(nil), l...)
				if len(c) > 0 {
					c[r.IntN(len(c))] = byte(r.IntN(256))
				}
				lines = append(lines, c)
			default:
				lines = append(lines, l)
			}
		}
		if iter%2 == 0 {
			r.Shuffle(len(lines), func(i, j int) { lines[i], lines[j] = lines[j], lines[i] })
		}
		n := New(Options{})
		for _, l := range lines {
			if _, err := n.Feed(n.Next(), l); err != nil {
				t.Fatal(err)
			}
		}
		validated(t, n)
		n.SetLive(false)
		validated(t, n)
	}
}
