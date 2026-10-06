package conversations

import (
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// scanHead opens path and continues a head scan from prev.
func scanHead(t *testing.T, path string, prev Head) (Head, int64) {
	t.Helper()
	f, _, err := OpenTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h, n, err := ScanHead(f, prev)
	if err != nil {
		t.Fatalf("ScanHead: %v", err)
	}
	return h, n
}

func TestScanHead_CwdAndEntrypointFromEarlyLines(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.jsonl")
	writeFile(t, p, lines(t,
		obj{"type": "summary", "summary": "s"},
		obj{"type": "system", "cwd": "", "entrypoint": "cli"},
		obj{"type": "attachment", "cwd": "/w/first", "entrypoint": "sdk-cli"},
		with(userText("hello"), obj{"cwd": "/w/second", "entrypoint": "sdk-ts"}),
	))

	h, n := scanHead(t, p, Head{})
	size := fileSize(t, p)
	want := Head{Cwd: "/w/first", FirstEntrypoint: "cli", FirstPrompt: "hello", Offset: size, Done: true}
	if h != want {
		t.Errorf("head = %+v, want %+v", h, want)
	}
	if n != size {
		t.Errorf("n = %d, want %d", n, size)
	}
}

func TestScanHead_SkipsLinesThatAreNotHumanPrompts(t *testing.T) {
	cases := []struct {
		name string
		skip []byte
	}{
		{"meta", line(t, with(userText("caveat"), obj{"isMeta": true}))},
		{"sidechain", line(t, with(userText("sub task"), obj{"isSidechain": true}))},
		{"compact summary", line(t, with(userText("This session is being continued"), obj{"isCompactSummary": true}))},
		{"command wrapper", line(t, userText("<command-name>/clear</command-name>"))},
		{"task notification", line(t, userText("<task-notification>done</task-notification>"))},
		{"pasted content", line(t, userText("<pasted_content id=\"1\">text</pasted_content>"))},
		{"tag after whitespace", line(t, userText(" \n\t<command-message>x</command-message>"))},
		{"tool result", line(t, userBlocks(
			obj{"type": "tool_result", "tool_use_id": "t1", "content": "ok"},
			obj{"type": "text", "text": "plain words"},
		))},
		{"first text block is a tag", line(t, userBlocks(
			obj{"type": "text", "text": "<system-reminder>r</system-reminder>"},
			obj{"type": "text", "text": "plain words"},
		))},
		{"blank text", line(t, userText(" \n\t "))},
		{"no text block", line(t, userBlocks(obj{"type": "image", "source": obj{"type": "base64", "data": "AA=="}}))},
		{"assistant", line(t, obj{"type": "assistant", "message": obj{"role": "assistant", "content": "words"}})},
		{"not an object", []byte("[\"user\"]\n")},
		{"not json", []byte("garbage\n")},
		{"empty line", []byte("\n")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "t.jsonl")
			writeFile(t, p, c.skip, line(t, userText("the real prompt")))
			h, _ := scanHead(t, p, Head{})
			if !h.Done || h.FirstPrompt != "the real prompt" {
				t.Errorf("head = %+v, want the real prompt, Done", h)
			}
		})
	}
}

func TestScanHead_CountsPromptForms(t *testing.T) {
	cases := []struct {
		name   string
		prompt obj
		want   string
	}{
		{"text blocks with an image", userBlocks(
			obj{"type": "image", "source": obj{"type": "base64", "data": "AA=="}},
			obj{"type": "text", "text": "see this"},
		), "see this"},
		{"leading whitespace", userText("  \n  look here  \n"), "look here"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "t.jsonl")
			writeFile(t, p, lines(t, c.prompt))
			h, _ := scanHead(t, p, Head{})
			if !h.Done || h.FirstPrompt != c.want {
				t.Errorf("head = %+v, want %q, Done", h, c.want)
			}
		})
	}
}

func TestScanHead_FindsPromptFarIntoTheFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.jsonl")
	first := line(t, obj{"type": "system", "cwd": "/w/far", "entrypoint": "cli"})
	pad := padding(t, 5<<20)
	prompt := line(t, userText("far prompt"))
	writeFile(t, p, first, pad, prompt, line(t, userText("second prompt")))

	h, _ := scanHead(t, p, Head{})
	end := int64(len(first) + len(pad) + len(prompt))
	want := Head{Cwd: "/w/far", FirstEntrypoint: "cli", FirstPrompt: "far prompt", Offset: end, Done: true}
	if h != want {
		t.Errorf("head = %+v, want %+v", h, want)
	}
}

func TestScanHead_ResumesWhereItStopped(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.jsonl")
	writeFile(t, p, lines(t,
		obj{"type": "system", "cwd": "/w/first", "entrypoint": "cli"},
		obj{"type": "assistant", "message": obj{"role": "assistant", "content": "hi"}},
		userBlocks(obj{"type": "tool_result", "tool_use_id": "t1", "content": "ok"}),
	))
	size := fileSize(t, p)

	h, n := scanHead(t, p, Head{})
	want := Head{Cwd: "/w/first", FirstEntrypoint: "cli", Offset: size}
	if h != want || n != size {
		t.Fatalf("first scan = %+v, %d; want %+v, %d", h, n, want, size)
	}

	appended := line(t, with(userText("now a prompt"), obj{"cwd": "/w/second", "entrypoint": "sdk-cli"}))
	appendFile(t, p, appended)
	h, n = scanHead(t, p, h)
	want = Head{Cwd: "/w/first", FirstEntrypoint: "cli", FirstPrompt: "now a prompt", Offset: size + int64(len(appended)), Done: true}
	if h != want {
		t.Errorf("resumed head = %+v, want %+v", h, want)
	}
	if n != int64(len(appended)) {
		t.Errorf("resumed n = %d, want %d (the appended bytes only)", n, len(appended))
	}
}

func TestScanHead_LeavesPartialLastLine(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.jsonl")
	first := line(t, obj{"type": "system", "cwd": "/w", "entrypoint": "cli"})
	// A complete prompt object still being written: no '\n' yet.
	partial := []byte(strings.TrimSuffix(string(line(t, userText("in flight"))), "\n"))
	writeFile(t, p, first, partial)

	h, n := scanHead(t, p, Head{})
	want := Head{Cwd: "/w", FirstEntrypoint: "cli", Offset: int64(len(first))}
	if h != want {
		t.Fatalf("head = %+v, want %+v", h, want)
	}
	if size := fileSize(t, p); n != size {
		t.Errorf("n = %d, want %d", n, size)
	}

	appendFile(t, p, []byte("\n"))
	h, n = scanHead(t, p, h)
	want = Head{Cwd: "/w", FirstEntrypoint: "cli", FirstPrompt: "in flight", Offset: fileSize(t, p), Done: true}
	if h != want {
		t.Errorf("resumed head = %+v, want %+v", h, want)
	}
	if n != int64(len(partial)+1) {
		t.Errorf("resumed n = %d, want %d", n, len(partial)+1)
	}
}

func TestScanHead_LinesLongerThanTheReadBuffer(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.jsonl")
	big := strings.Repeat("y", 3*headBufSize)
	first := line(t, obj{"type": "assistant", "cwd": "/w/long", "entrypoint": "cli", "message": obj{"content": big}})
	second := line(t, userBlocks(obj{"type": "tool_result", "tool_use_id": "t1", "content": big}))
	partial := []byte(strings.TrimSuffix(string(line(t, userText("long prompt "+big))), "\n"))
	writeFile(t, p, first, second, partial)

	h, _ := scanHead(t, p, Head{})
	want := Head{Cwd: "/w/long", FirstEntrypoint: "cli", Offset: int64(len(first) + len(second))}
	if h != want {
		t.Fatalf("head = %+v, want %+v", h, want)
	}

	appendFile(t, p, []byte("\n"))
	h, _ = scanHead(t, p, h)
	want = Head{Cwd: "/w/long", FirstEntrypoint: "cli", FirstPrompt: ("long prompt " + big)[:500], Offset: fileSize(t, p), Done: true}
	if h != want {
		t.Errorf("resumed head = %+v, want %+v", h, want)
	}
}

func TestScanHead_CutsLongPromptOnRuneBoundary(t *testing.T) {
	cases := map[string]struct {
		text    string
		wantLen int
	}{
		"ascii":     {strings.Repeat("a", 600), 500},
		"multibyte": {strings.Repeat("中", 200), 498}, // 3 bytes each: 166 runes fit
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "t.jsonl")
			writeFile(t, p, lines(t, userText(c.text)))
			h, _ := scanHead(t, p, Head{})
			got := h.FirstPrompt
			if len(got) != c.wantLen || !utf8.ValidString(got) || !strings.HasPrefix(c.text, got) {
				t.Errorf("prompt = %d bytes (valid UTF-8 %v), want a %d-byte prefix", len(got), utf8.ValidString(got), c.wantLen)
			}
		})
	}
}

func TestScanHead_StopsAtTheCap(t *testing.T) {
	old := headCap
	headCap = 4 << 10
	t.Cleanup(func() { headCap = old })

	p := filepath.Join(t.TempDir(), "t.jsonl")
	first := line(t, obj{"type": "system", "cwd": "/w/cap", "entrypoint": "cli"})
	pad := padding(t, 4000-len(first))
	// This prompt starts below the cap and ends above it.
	straddling := line(t, userText(strings.Repeat("s", 300)))
	writeFile(t, p, first, pad, straddling, line(t, userText("after the cap")))
	if start := len(first) + len(pad); start >= 4<<10 || start+len(straddling) <= 4<<10 {
		t.Fatalf("fixture: the prompt spans [%d, %d), want it across %d", start, start+len(straddling), 4<<10)
	}

	h, n := scanHead(t, p, Head{})
	want := Head{Cwd: "/w/cap", FirstEntrypoint: "cli", Offset: 4000, Done: true}
	if h != want {
		t.Errorf("head = %+v, want %+v", h, want)
	}
	if n > 4<<10 {
		t.Errorf("n = %d, want at most the cap %d", n, 4<<10)
	}
}

func TestScanHead_DoneHeadIsNotReadAgain(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.jsonl")
	writeFile(t, p, lines(t, userText("first"), userText("second")))
	prev := Head{FirstPrompt: "first", Offset: 10, Done: true}

	h, n := scanHead(t, p, prev)
	if h != prev || n != 0 {
		t.Errorf("ScanHead(done) = %+v, %d; want %+v, 0", h, n, prev)
	}
}
