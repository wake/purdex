package conversations

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wake/purdex/internal/store"
)

// fakeIndex is an in-memory Index: rows by session id, stored exactly as
// given, and the rows of every UpsertBatch call.
type fakeIndex struct {
	rows      map[string]store.ConversationIndexRow
	upserts   [][]store.ConversationIndexRow
	allErr    error
	upsertErr error
}

func newFakeIndex() *fakeIndex { return &fakeIndex{rows: map[string]store.ConversationIndexRow{}} }

func (x *fakeIndex) All(context.Context) ([]store.ConversationIndexRow, error) {
	if x.allErr != nil {
		return nil, x.allErr
	}
	out := make([]store.ConversationIndexRow, 0, len(x.rows))
	for _, r := range x.rows {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SessionID < out[j].SessionID })
	return out, nil
}

func (x *fakeIndex) UpsertBatch(_ context.Context, rows []store.ConversationIndexRow) error {
	x.upserts = append(x.upserts, append([]store.ConversationIndexRow(nil), rows...))
	if x.upsertErr != nil {
		return x.upsertErr
	}
	for _, r := range rows {
		x.rows[r.SessionID] = r
	}
	return nil
}

// batch is the rows of UpsertBatch call i (from 0).
func (x *fakeIndex) batch(t *testing.T, i int) []store.ConversationIndexRow {
	t.Helper()
	if i >= len(x.upserts) {
		t.Fatalf("%d UpsertBatch calls, want call %d", len(x.upserts), i+1)
	}
	return x.upserts[i]
}

var (
	t0 = time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	t1 = t0.Add(time.Hour)
)

func at(tm time.Time) func() time.Time { return func() time.Time { return tm } }

// runScan scans root at tm and fails the test on an error.
func runScan(t *testing.T, root string, idx Index, tm time.Time) ScanResult {
	t.Helper()
	res, err := Scan(context.Background(), root, idx, at(tm))
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return res
}

// hookOpen makes Scan open transcripts through fn for the rest of the test.
func hookOpen(t *testing.T, fn func(string) (*os.File, Entry, error)) {
	t.Helper()
	old := openTranscript
	openTranscript = fn
	t.Cleanup(func() { openTranscript = old })
}

// tailBytes is what ReadTail reads of a file of the given size.
func tailBytes(size int64) int64 {
	if size > TailWindow {
		return TailWindow + 1 // the byte before the window too
	}
	return size
}

// wantRow is the row of path as it is on disk now, with head h and tail tl.
func wantRow(t *testing.T, sid, path string, h Head, tl Tail, firstSeen, lastSeen time.Time) store.ConversationIndexRow {
	t.Helper()
	e := statEntry(t, sid, path)
	return store.ConversationIndexRow{
		SessionID: sid, TranscriptPath: path,
		Cwd: h.Cwd, FirstEntrypoint: h.FirstEntrypoint, FirstPrompt: h.FirstPrompt,
		LastEntrypoint: tl.LastEntrypoint, CustomTitle: tl.CustomTitle, AITitle: tl.AITitle,
		Size: e.Size, MtimeMs: e.MtimeMs, Inode: e.Inode,
		HeadOffset: h.Offset, HeadDone: h.Done,
		FirstSeenAt: firstSeen.UnixMilli(), LastSeenAt: lastSeen.UnixMilli(),
	}
}

// fixtureRoot writes sidA (a prompt and titles) in -w-one and sidB (no
// prompt yet) in -w-two.
func fixtureRoot(t *testing.T) (root, a, b string) {
	t.Helper()
	root = t.TempDir()
	a = filepath.Join(root, "-w-one", sidA+".jsonl")
	b = filepath.Join(root, "-w-two", sidB+".jsonl")
	writeFile(t, a, lines(t,
		obj{"type": "system", "cwd": "/w/one", "entrypoint": "cli"},
		userText("hello"),
		aiTitle("ai title"),
		customTitle("custom title"),
		obj{"type": "assistant", "entrypoint": "sdk-cli"},
	))
	writeFile(t, b, lines(t,
		obj{"type": "attachment", "cwd": "/w/two", "entrypoint": "sdk-cli"},
		obj{"type": "assistant", "message": obj{"content": "no prompt yet"}},
	))
	return root, a, b
}

func TestScan_FirstScanIndexesEveryFile(t *testing.T) {
	root, a, b := fixtureRoot(t)
	idx := newFakeIndex()
	res := runScan(t, root, idx, t0)

	ha, han := scanHead(t, a, Head{})
	ta, tan := readTail(t, a, -1)
	hb, hbn := scanHead(t, b, Head{})
	tb, tbn := readTail(t, b, -1)
	if ha.FirstPrompt != "hello" || !ha.Done || ta.CustomTitle != "custom title" || hb.Done || hb.Cwd != "/w/two" {
		t.Fatalf("fixture: heads %+v %+v, tail %+v", ha, hb, ta)
	}
	want := []store.ConversationIndexRow{
		wantRow(t, sidA, a, ha, ta, t0, t0),
		wantRow(t, sidB, b, hb, tb, t0, t0),
	}
	if len(idx.upserts) != 1 || !reflect.DeepEqual(idx.upserts[0], want) {
		t.Errorf("upserts = %+v, want one batch %+v", idx.upserts, want)
	}
	wantPresent := map[string]Entry{sidA: statEntry(t, sidA, a), sidB: statEntry(t, sidB, b)}
	if !reflect.DeepEqual(res.Present, wantPresent) {
		t.Errorf("Present = %+v, want %+v", res.Present, wantPresent)
	}
	if res.RootErr != nil || len(res.UnreadableDirs) != 0 || res.ScannedAt != t0.UnixMilli() {
		t.Errorf("RootErr %v, UnreadableDirs %v, ScannedAt %d", res.RootErr, res.UnreadableDirs, res.ScannedAt)
	}
	if res.Files != 2 || res.Reread != 2 || res.BytesRead != han+tan+hbn+tbn {
		t.Errorf("Files %d, Reread %d, BytesRead %d; want 2, 2, %d", res.Files, res.Reread, res.BytesRead, han+tan+hbn+tbn)
	}
}

func TestScan_UnchangedFileIsNotReadAgain(t *testing.T) {
	root, _, _ := fixtureRoot(t)
	idx := newFakeIndex()
	runScan(t, root, idx, t0)
	first := idx.batch(t, 0)

	res := runScan(t, root, idx, t1)
	if res.Files != 2 || res.Reread != 0 || res.BytesRead != 0 {
		t.Errorf("Files %d, Reread %d, BytesRead %d; want 2, 0, 0", res.Files, res.Reread, res.BytesRead)
	}
	want := append([]store.ConversationIndexRow(nil), first...)
	for i := range want {
		want[i].LastSeenAt = t1.UnixMilli()
	}
	if got := idx.batch(t, 1); len(idx.upserts) != 2 || !reflect.DeepEqual(got, want) {
		t.Errorf("second batch = %+v, want %+v (LastSeenAt bumped only)", got, want)
	}
}

func TestScan_GrownFileWithAFinishedHeadReadsOnlyTheTail(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "-w", sidA+".jsonl")
	writeFile(t, p, lines(t, obj{"type": "system", "cwd": "/w/a", "entrypoint": "cli"}, userText("first prompt")), padding(t, 300<<10))
	idx := newFakeIndex()
	runScan(t, root, idx, t0)
	before := idx.rows[sidA]
	if !before.HeadDone || before.FirstPrompt != "first prompt" {
		t.Fatalf("fixture: first row %+v", before)
	}

	appendFile(t, p, lines(t, with(userText("later prompt"), obj{"cwd": "/w/elsewhere", "entrypoint": "sdk-cli"}), aiTitle("grown")))
	res := runScan(t, root, idx, t1)
	if size := fileSize(t, p); res.Reread != 1 || res.BytesRead != tailBytes(size) {
		t.Errorf("Reread %d, BytesRead %d; want 1, %d (the tail only)", res.Reread, res.BytesRead, tailBytes(size))
	}
	e := statEntry(t, sidA, p)
	want := before
	want.LastEntrypoint, want.AITitle = "sdk-cli", "grown"
	want.Size, want.MtimeMs, want.LastSeenAt = e.Size, e.MtimeMs, t1.UnixMilli()
	if got := idx.rows[sidA]; got != want {
		t.Errorf("row = %+v\nwant  %+v", got, want)
	}
}

func TestScan_GrownFileWithAnUnfinishedHeadResumesIt(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "-w", sidA+".jsonl")
	writeFile(t, p, lines(t, obj{"type": "system", "cwd": "/w/a", "entrypoint": "cli"}), padding(t, 300<<10),
		line(t, userBlocks(obj{"type": "tool_result", "tool_use_id": "t1", "content": "ok"})))
	size0 := fileSize(t, p)
	idx := newFakeIndex()
	runScan(t, root, idx, t0)
	if r := idx.rows[sidA]; r.HeadDone || r.HeadOffset != size0 {
		t.Fatalf("fixture: first row %+v, want an unfinished head at %d", r, size0)
	}

	prompt := line(t, with(userText("late prompt"), obj{"cwd": "/w/b", "entrypoint": "sdk-cli"}))
	appended := join(line(t, obj{"type": "assistant"}), prompt, line(t, userText("second prompt")))
	appendFile(t, p, appended)
	res := runScan(t, root, idx, t1)
	size := fileSize(t, p)
	if wantRead := int64(len(appended)) + tailBytes(size); res.Reread != 1 || res.BytesRead != wantRead {
		t.Errorf("Reread %d, BytesRead %d; want 1, %d (the appended bytes and the tail)", res.Reread, res.BytesRead, wantRead)
	}
	h := Head{Cwd: "/w/a", FirstEntrypoint: "cli", FirstPrompt: "late prompt", Offset: size - int64(len(line(t, userText("second prompt")))), Done: true}
	if got, want := idx.rows[sidA], wantRow(t, sidA, p, h, Tail{LastEntrypoint: "sdk-cli"}, t0, t1); got != want {
		t.Errorf("row = %+v\nwant  %+v", got, want)
	}
}

func TestScan_RewrittenFileReadsTheHeadFromZero(t *testing.T) {
	v1 := lines(t, obj{"type": "system", "cwd": "/w/old", "entrypoint": "cli"}, userText("old prompt"), aiTitle("old title"))
	v2 := lines(t, obj{"type": "system", "cwd": "/w/new", "entrypoint": "sdk-cli"}, userText("new prompt"))
	cases := map[string]struct {
		before, after []byte
		rewrite       func(t *testing.T, p string, content []byte)
		sameInode     bool // and smaller; otherwise a new inode and larger
	}{
		"shrank": {join(v1, padding(t, 10<<10)), v2, func(t *testing.T, p string, c []byte) {
			if err := os.WriteFile(p, c, 0o644); err != nil { // truncates in place
				t.Fatal(err)
			}
		}, true},
		"new inode": {v1, join(v2, padding(t, 10<<10)), func(t *testing.T, p string, c []byte) {
			tmp := filepath.Join(filepath.Dir(p), "rewrite.tmp")
			writeFile(t, tmp, c)
			if err := os.Rename(tmp, p); err != nil {
				t.Fatal(err)
			}
		}, false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			p := filepath.Join(root, "-w", sidA+".jsonl")
			writeFile(t, p, c.before)
			idx := newFakeIndex()
			runScan(t, root, idx, t0)
			before := idx.rows[sidA]

			c.rewrite(t, p, c.after)
			e := statEntry(t, sidA, p)
			if (e.Inode == before.Inode) != c.sameInode || (e.Size < before.Size) != c.sameInode {
				t.Fatalf("fixture: inode %d→%d, size %d→%d", before.Inode, e.Inode, before.Size, e.Size)
			}
			res := runScan(t, root, idx, t1)
			h, hn := scanHead(t, p, Head{})
			tl, tn := readTail(t, p, -1)
			if h.Cwd != "/w/new" || h.FirstPrompt != "new prompt" || tl.AITitle != "" {
				t.Fatalf("oracle: head %+v, tail %+v", h, tl)
			}
			if got, want := idx.rows[sidA], wantRow(t, sidA, p, h, tl, t0, t1); got != want {
				t.Errorf("row = %+v\nwant  %+v", got, want)
			}
			if res.Reread != 1 || res.BytesRead != hn+tn {
				t.Errorf("Reread %d, BytesRead %d; want 1, %d", res.Reread, res.BytesRead, hn+tn)
			}
		})
	}
}

func TestScan_TouchedFileRereadsTheTailOnly(t *testing.T) {
	// Same size, new mtime: not unchanged, and not shrunk.
	root, a, _ := fixtureRoot(t)
	idx := newFakeIndex()
	runScan(t, root, idx, t0)
	before := idx.rows[sidA]
	if err := os.Chtimes(a, t1, t1); err != nil {
		t.Fatal(err)
	}
	res := runScan(t, root, idx, t1)
	if res.Reread != 1 || res.BytesRead != tailBytes(before.Size) {
		t.Errorf("Reread %d, BytesRead %d; want 1, %d", res.Reread, res.BytesRead, tailBytes(before.Size))
	}
	want := before
	want.MtimeMs, want.LastSeenAt = t1.UnixMilli(), t1.UnixMilli()
	if got := idx.rows[sidA]; got != want {
		t.Errorf("row = %+v\nwant  %+v", got, want)
	}
}

func TestScan_RenamedSlugDirKeepsTheRowAndTheNewPath(t *testing.T) {
	root, a, _ := fixtureRoot(t)
	idx := newFakeIndex()
	runScan(t, root, idx, t0)
	before := idx.rows[sidA]
	moved := filepath.Join(root, "-w-renamed")
	if err := os.Rename(filepath.Dir(a), moved); err != nil {
		t.Fatal(err)
	}
	res := runScan(t, root, idx, t1)
	if res.Reread != 0 || res.BytesRead != 0 {
		t.Errorf("Reread %d, BytesRead %d; want 0, 0", res.Reread, res.BytesRead)
	}
	want := before
	want.TranscriptPath, want.LastSeenAt = filepath.Join(moved, sidA+".jsonl"), t1.UnixMilli()
	if got := idx.rows[sidA]; got != want {
		t.Errorf("row = %+v\nwant  %+v", got, want)
	}
}

func TestScan_IndexRowIDIsMatchedCaseInsensitively(t *testing.T) {
	root, _, _ := fixtureRoot(t)
	idx := newFakeIndex()
	runScan(t, root, idx, t0)
	row := idx.rows[sidA]
	delete(idx.rows, sidA)
	row.SessionID = strings.ToUpper(sidA)
	idx.rows[row.SessionID] = row

	res := runScan(t, root, idx, t1)
	if res.Reread != 0 {
		t.Errorf("Reread %d, want 0 (the upper-case row matches the unchanged file)", res.Reread)
	}
	if batch := idx.batch(t, 1); batch[0].SessionID != sidA || batch[0].FirstSeenAt != t0.UnixMilli() {
		t.Errorf("written row %+v, want the lower-case id and the stored row's FirstSeenAt", batch[0])
	}
}

func TestScan_RemovedFileIsAbsentAndItsRowUntouched(t *testing.T) {
	root, _, b := fixtureRoot(t)
	idx := newFakeIndex()
	runScan(t, root, idx, t0)
	rowB := idx.rows[sidB]
	if err := os.Remove(b); err != nil {
		t.Fatal(err)
	}
	res := runScan(t, root, idx, t1)
	if _, ok := res.Present[sidB]; ok || len(res.Present) != 1 || res.Files != 1 {
		t.Errorf("Present = %+v, Files %d; want sidA only", res.Present, res.Files)
	}
	if batch := idx.batch(t, 1); len(batch) != 1 || batch[0].SessionID != sidA {
		t.Errorf("second batch = %+v, want sidA's row only", batch)
	}
	if got := idx.rows[sidB]; got != rowB {
		t.Errorf("row of the removed file = %+v, want %+v", got, rowB)
	}
}

func TestScan_MissingRootWritesNothing(t *testing.T) {
	idx := newFakeIndex()
	idx.rows[sidA] = store.ConversationIndexRow{SessionID: sidA, TranscriptPath: "/r/-w/" + sidA + ".jsonl", LastSeenAt: 1}
	res, err := Scan(context.Background(), filepath.Join(t.TempDir(), "missing"), idx, at(t0))
	if err != nil {
		t.Fatalf("Scan: %v, want the root error in the result", err)
	}
	if res.RootErr == nil || res.Present != nil || res.Files != 0 || res.ScannedAt != t0.UnixMilli() {
		t.Errorf("result = %+v, want RootErr, nil Present, ScannedAt %d", res, t0.UnixMilli())
	}
	if len(idx.upserts) != 0 || idx.rows[sidA].LastSeenAt != 1 {
		t.Errorf("upserts = %+v, want none", idx.upserts)
	}
}

func TestScan_UnreadableSlugDirKeepsItsRows(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a chmod 000 dir")
	}
	root, a, b := fixtureRoot(t)
	idx := newFakeIndex()
	runScan(t, root, idx, t0)
	rowA := idx.rows[sidA]
	appendFile(t, b, line(t, userText("now a prompt")))
	locked := filepath.Dir(a)
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })

	res := runScan(t, root, idx, t1)
	if want := []string{locked}; !reflect.DeepEqual(res.UnreadableDirs, want) {
		t.Errorf("UnreadableDirs = %v, want %v", res.UnreadableDirs, want)
	}
	if _, ok := res.Present[sidA]; ok {
		t.Errorf("Present = %+v, want no sidA (its dir was not listed)", res.Present)
	}
	if got := idx.rows[sidA]; got != rowA {
		t.Errorf("row in the unreadable dir = %+v, want %+v", got, rowA)
	}
	if res.Reread != 1 || idx.rows[sidB].FirstPrompt != "now a prompt" {
		t.Errorf("Reread %d, sidB row %+v; want the other file scanned", res.Reread, idx.rows[sidB])
	}
}

func TestScan_FileThatFailsToOpenIsSkipped(t *testing.T) {
	root, a, b := fixtureRoot(t)
	idx := newFakeIndex()
	runScan(t, root, idx, t0)
	rowA := idx.rows[sidA]
	appendFile(t, a, line(t, aiTitle("never read")))
	appendFile(t, b, line(t, userText("now a prompt")))
	hookOpen(t, func(path string) (*os.File, Entry, error) {
		if path == a { // replaced by a FIFO between the listing and the open
			if err := os.Remove(a); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(a, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return OpenTranscript(path)
	})

	res := runScan(t, root, idx, t1)
	if _, ok := res.Present[sidA]; !ok {
		t.Errorf("Present = %+v, want sidA still present", res.Present)
	}
	if batch := idx.batch(t, 1); len(batch) != 1 || batch[0].SessionID != sidB || batch[0].FirstPrompt != "now a prompt" {
		t.Errorf("second batch = %+v, want sidB's row only", batch)
	}
	if got := idx.rows[sidA]; got != rowA {
		t.Errorf("row of the unopenable file = %+v, want %+v", got, rowA)
	}
}

func TestScan_FileAppendedAroundTheOpen(t *testing.T) {
	// The row's Size is the opened file's fstat size, not the listing's. The
	// head scan reads up to the cap, not to that size: it may store a
	// HeadOffset past the stored Size. The next scan sees a grown file.
	root := t.TempDir()
	p := filepath.Join(root, "-w", sidA+".jsonl")
	first := line(t, obj{"type": "system", "cwd": "/w/a", "entrypoint": "cli"})
	early := line(t, aiTitle("written before the open"))
	racing := line(t, aiTitle("written during the scan"))
	writeFile(t, p, first)
	hookOpen(t, func(path string) (*os.File, Entry, error) {
		appendFile(t, path, early)
		f, e, err := OpenTranscript(path)
		appendFile(t, path, racing)
		return f, e, err
	})
	idx := newFakeIndex()
	res := runScan(t, root, idx, t0)
	if got := res.Present[sidA].Size; got != int64(len(first)) {
		t.Errorf("Present size %d, want the listing's %d", got, len(first))
	}
	r := idx.rows[sidA]
	opened, headEnd := int64(len(first)+len(early)), int64(len(first)+len(early)+len(racing))
	if r.Size != opened || r.HeadOffset != headEnd || r.HeadDone || r.AITitle != "written before the open" {
		t.Fatalf("first row: Size %d, HeadOffset %d, HeadDone %v, AITitle %q; want %d, %d, false, the title before the open",
			r.Size, r.HeadOffset, r.HeadDone, r.AITitle, opened, headEnd)
	}

	hookOpen(t, OpenTranscript)
	prompt := line(t, userText("the prompt"))
	appendFile(t, p, prompt)
	res = runScan(t, root, idx, t1)
	size := fileSize(t, p)
	if wantRead := int64(len(prompt)) + tailBytes(size); res.BytesRead != wantRead {
		t.Errorf("BytesRead %d, want %d (the head resumed from its offset)", res.BytesRead, wantRead)
	}
	h := Head{Cwd: "/w/a", FirstEntrypoint: "cli", FirstPrompt: "the prompt", Offset: size, Done: true}
	if got, want := idx.rows[sidA], wantRow(t, sidA, p, h, Tail{LastEntrypoint: "cli", AITitle: "written during the scan"}, t0, t1); got != want {
		t.Errorf("row = %+v\nwant  %+v", got, want)
	}
}

func TestScan_FileShorterThanItsHeadOffsetIsRewritten(t *testing.T) {
	// A growth during the scan stores HeadOffset > Size; a rewrite in place
	// (same inode) to a length between them is not smaller than Size, but
	// shorter than bytes already read: the head is read again from 0.
	root := t.TempDir()
	p := filepath.Join(root, "-w", sidA+".jsonl")
	writeFile(t, p, line(t, obj{"type": "system", "cwd": "/w/old", "entrypoint": "cli"}))
	hookOpen(t, func(path string) (*os.File, Entry, error) {
		f, e, err := OpenTranscript(path)
		appendFile(t, path, padding(t, 4<<10))
		return f, e, err
	})
	idx := newFakeIndex()
	runScan(t, root, idx, t0)
	before := idx.rows[sidA]
	hookOpen(t, OpenTranscript)

	fresh := lines(t, obj{"type": "system", "cwd": "/w/new", "entrypoint": "sdk-cli"}, userText("new prompt"))
	if err := os.Truncate(p, 0); err != nil {
		t.Fatal(err)
	}
	appendFile(t, p, fresh)
	e := statEntry(t, sidA, p)
	if e.Inode != before.Inode || e.Size <= before.Size || e.Size >= before.HeadOffset {
		t.Fatalf("fixture: inode %d→%d, size %d; want the same inode and a size in (%d, %d)",
			before.Inode, e.Inode, e.Size, before.Size, before.HeadOffset)
	}

	res := runScan(t, root, idx, t1)
	h := Head{Cwd: "/w/new", FirstEntrypoint: "sdk-cli", FirstPrompt: "new prompt", Offset: int64(len(fresh)), Done: true}
	if got, want := idx.rows[sidA], wantRow(t, sidA, p, h, Tail{LastEntrypoint: "sdk-cli"}, t0, t1); got != want {
		t.Errorf("row = %+v\nwant  %+v", got, want)
	}
	if res.Reread != 1 || res.BytesRead != 2*int64(len(fresh)) {
		t.Errorf("Reread %d, BytesRead %d; want 1, %d (head from 0 and tail)", res.Reread, res.BytesRead, 2*len(fresh))
	}
}

func TestScan_CancelledContextStopsBetweenFiles(t *testing.T) {
	root, _, _ := fixtureRoot(t)
	writeFile(t, filepath.Join(root, "-w-three", sidC+".jsonl"), lines(t, userText("c")))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var opened []string
	hookOpen(t, func(path string) (*os.File, Entry, error) {
		opened = append(opened, path)
		cancel() // during the first file
		return OpenTranscript(path)
	})
	idx := newFakeIndex()
	_, err := Scan(ctx, root, idx, at(t0))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if len(opened) != 1 || len(idx.upserts) != 0 {
		t.Errorf("opened %v, upserts %d; want one file opened, nothing written", opened, len(idx.upserts))
	}
}

func TestScan_IndexErrorsAreReturned(t *testing.T) {
	boom := errors.New("boom")
	for name, idx := range map[string]*fakeIndex{
		"All":         {rows: map[string]store.ConversationIndexRow{}, allErr: boom},
		"UpsertBatch": {rows: map[string]store.ConversationIndexRow{}, upsertErr: boom},
	} {
		t.Run(name, func(t *testing.T) {
			root, _, _ := fixtureRoot(t)
			if _, err := Scan(context.Background(), root, idx, at(t0)); !errors.Is(err, boom) {
				t.Errorf("err = %v, want %v", err, boom)
			}
			if len(idx.rows) != 0 {
				t.Errorf("rows = %+v, want none written", idx.rows)
			}
		})
	}
}
