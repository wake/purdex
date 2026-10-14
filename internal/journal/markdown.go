package journal

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ErrNotFound is returned when an entry id is not in the file.
var ErrNotFound = errors.New("journal: entry not found")

// ErrCorrupt is returned when the file holds a metadata comment naming the entry that cannot be read; writing would
// duplicate or hide it, so the file is left alone.
var ErrCorrupt = errors.New("journal: unreadable metadata for entry")

var (
	lockMu sync.Mutex
	locks  = map[string]*sync.Mutex{}
)

// canonicalPath makes equivalent spellings of one path equal: absolute, cleaned, and with symlinks resolved on the
// nearest existing ancestor, so the answer is the same before and after the file or its directory is created.
func canonicalPath(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	rest := ""
	for p := path; ; p = filepath.Dir(p) {
		if real, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(real, rest)
		}
		if filepath.Dir(p) == p {
			return path
		}
		rest = filepath.Join(filepath.Base(p), rest)
	}
}

// lockFile serialises read-modify-write of one file inside this process (the daemon is the only writer).
func lockFile(path string) func() {
	path = canonicalPath(path)
	lockMu.Lock()
	l, ok := locks[path]
	if !ok {
		l = &sync.Mutex{}
		locks[path] = l
	}
	lockMu.Unlock()
	l.Lock()
	return l.Unlock
}

// corruptFor reports a metadata line that does not parse but names id.
func corruptFor(lines []string, id string) bool {
	q, _ := json.Marshal(id)
	for _, l := range lines {
		if strings.HasPrefix(l, metaPrefix) && strings.Contains(l, strings.ReplaceAll(`"id":`+string(q), "--", dashEsc+dashEsc)) {
			if _, err := parseMeta(l); err != nil {
				return true
			}
		}
	}
	return false
}

const (
	metaPrefix = "<!-- j "
	// dashEsc is the JSON escape of "-" (backslash, u, 002d).
	dashEsc = "\x5c" + "u002d"
)

// MonthFile is the journal file of the month of at, inside dir (<dir>/<YYYY-MM>.md).
func MonthFile(dir string, at time.Time) string {
	return filepath.Join(dir, at.Format("2006-01")+".md")
}

// metaLine is the machine-readable comment of an entry. encoding/json already writes <, > and & as \u00XX, so the
// only way a JSON string could end the comment is "--", whose two dashes are written as JSON escapes.
func metaLine(e Entry) (string, error) {
	s, err := commentJSON(e)
	if err != nil {
		return "", err
	}
	return metaPrefix + s + " -->", nil
}

// commentJSON is v as JSON that is safe inside an HTML comment.
func commentJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(string(b), "--", dashEsc+dashEsc), nil
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func splitText(s string) []string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	return strings.Split(s, "\n")
}

// safeLine keeps a line of free text from reading as structure (heading, list, quote, table, fence, comment).
func safeLine(l string) string {
	t := strings.TrimLeft(l, " \t")
	if t == "" {
		return l
	}
	if strings.ContainsRune("#-*+><|`=~\\[", rune(t[0])) {
		return `\` + l
	}
	i := 0
	for i < len(t) && t[i] >= '0' && t[i] <= '9' {
		i++
	}
	if i > 0 && i < len(t) && (t[i] == '.' || t[i] == ')') {
		return `\` + l
	}
	return l
}

func longestBacktickRun(s string) int {
	best, cur := 0, 0
	for _, r := range s {
		if r == '`' {
			cur++
			if cur > best {
				best = cur
			}
		} else {
			cur = 0
		}
	}
	return best
}

// render is the block of an entry: heading, visible rendering, metadata comment.
func render(e Entry) ([]string, error) {
	meta, err := metaLine(e)
	if err != nil {
		return nil, err
	}
	head := "### " + e.At.Format("15:04") + " · " + oneLine(e.Label())
	if a := oneLine(e.Actor); a != "" {
		head += " · " + a
	}
	if e.Pinned {
		head += " 📌"
	}
	lines := []string{head}
	if e.Text != "" {
		for _, l := range splitText(e.Text) {
			lines = append(lines, safeLine(l))
		}
	}
	for _, it := range e.Items {
		box := "[ ]"
		if it.Done {
			box = "[x]"
		}
		lines = append(lines, "- "+box+" "+oneLine(it.Text))
	}
	if e.Detail != "" {
		fence := strings.Repeat("`", max(3, longestBacktickRun(e.Detail)+1))
		lines = append(lines, "<details><summary>原始提問</summary>", "", fence)
		for _, l := range splitText(e.Detail) {
			if t := strings.TrimLeft(l, " \t"); strings.HasPrefix(t, "#") || strings.HasPrefix(t, "<") {
				l = `\` + l
			}
			lines = append(lines, l)
		}
		lines = append(lines, fence, "", "</details>")
	}
	return append(lines, meta), nil
}

func readLines(file string) ([]string, error) {
	b, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return splitLines(string(b)), nil
}

// splitLines is the lines of a file's text, without its final newline.
func splitLines(text string) []string {
	s := strings.TrimSuffix(text, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// atomicWrite writes data to path through a temp file in the same directory and a rename.
func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	mode := os.FileMode(0o644)
	if st, serr := os.Stat(path); serr == nil {
		mode = st.Mode().Perm()
	}
	_ = tmp.Chmod(mode)
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	if err != nil {
		_ = os.Remove(name)
	}
	return err
}

func writeLines(file string, lines []string) error {
	return atomicWrite(file, []byte(strings.Join(lines, "\n")+"\n"))
}

// parseMeta reads the entry out of a metadata line.
func parseMeta(line string) (Entry, error) {
	line = strings.TrimSuffix(line, "\r")
	if !strings.HasSuffix(line, " -->") {
		return Entry{}, errors.New("comment not closed")
	}
	var e Entry
	dec := json.NewDecoder(strings.NewReader(strings.TrimSuffix(strings.TrimPrefix(line, metaPrefix), " -->")))
	if err := dec.Decode(&e); err != nil {
		return Entry{}, err
	}
	if e.ID == "" {
		return Entry{}, errors.New("entry without id")
	}
	return e, nil
}

// Parse reads the entries of a journal file from their metadata comments only; the visible text is never read.
// A comment that does not parse is skipped and logged with its line. A missing file has no entries.
func Parse(file string) ([]Entry, error) {
	lines, err := readLines(file)
	if err != nil {
		return nil, err
	}
	return parseLines(file, lines), nil
}

// parseLines reads the entries out of the lines of file's text (file only names it in the log).
func parseLines(file string, lines []string) []Entry {
	var out []Entry
	for i, l := range lines {
		if !strings.HasPrefix(l, metaPrefix) {
			continue
		}
		e, err := parseMeta(l)
		if err != nil {
			log.Printf("journal: %s:%d: skipping unreadable entry: %v", file, i+1, err)
			continue
		}
		out = append(out, e)
	}
	return out
}

// locate finds the block of id: [start, end] line indexes, end being the metadata line.
func locate(lines []string, id string) (start, end int, e Entry, ok bool) {
	boundary := -1
	for i, l := range lines {
		if !strings.HasPrefix(l, metaPrefix) {
			continue
		}
		if got, err := parseMeta(l); err == nil && got.ID == id {
			start = i
			// The nearest heading above the metadata is this entry's own; anything the user wrote between
			// entries sits above it and stays.
			for j := i - 1; j > boundary; j-- {
				if strings.HasPrefix(lines[j], "### ") {
					start = j
					break
				}
			}
			return start, i, got, true
		}
		boundary = i
	}
	return 0, 0, Entry{}, false
}

// Upsert appends e to file when its id is new, else re-renders that entry's block in place. Nothing else in the file
// changes.
func Upsert(file string, e Entry) error {
	if err := e.Validate(); err != nil {
		return err
	}
	block, err := render(e)
	if err != nil {
		return err
	}
	defer lockFile(file)()
	lines, err := readLines(file)
	if err != nil {
		return err
	}
	if corruptFor(lines, e.ID) {
		return fmt.Errorf("%w: %s", ErrCorrupt, e.ID)
	}
	if start, end, _, ok := locate(lines, e.ID); ok {
		out := append(append(append([]string{}, lines[:start]...), block...), lines[end+1:]...)
		return writeLines(file, out)
	}
	day := "## " + e.At.Format("2006-01-02")
	last := ""
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(lines[i], "## ") {
			last = lines[i]
			break
		}
	}
	if len(lines) > 0 {
		lines = append(lines, "")
	}
	if last != day {
		lines = append(lines, day, "")
	}
	return writeLines(file, append(lines, block...))
}

// update applies fn to the entry id and re-renders its block when fn reports a change.
func update(file, id string, fn func(*Entry) (bool, error)) (bool, error) {
	defer lockFile(file)()
	lines, err := readLines(file)
	if err != nil {
		return false, err
	}
	start, end, e, ok := locate(lines, id)
	if !ok {
		if corruptFor(lines, id) {
			return false, fmt.Errorf("%w: %s", ErrCorrupt, id)
		}
		return false, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	changed, err := fn(&e)
	if err != nil || !changed {
		return false, err
	}
	block, err := render(e)
	if err != nil {
		return false, err
	}
	out := append(append(append([]string{}, lines[:start]...), block...), lines[end+1:]...)
	return true, writeLines(file, out)
}

// Tick sets item idx of a confirm entry done. It reports whether anything changed.
func Tick(file, id string, idx int) (bool, error) {
	return update(file, id, func(e *Entry) (bool, error) {
		if idx < 0 || idx >= len(e.Items) {
			return false, fmt.Errorf("journal: entry %s has no item %d", id, idx)
		}
		if e.Items[idx].Done {
			return false, nil
		}
		e.Items[idx].Done = true
		return true, nil
	})
}

// SetPinned pins or unpins an entry. It reports whether anything changed.
func SetPinned(file, id string, pinned bool) (bool, error) {
	return update(file, id, func(e *Entry) (bool, error) {
		if e.Pinned == pinned {
			return false, nil
		}
		e.Pinned = pinned
		return true, nil
	})
}

// FillDetail sets the detail of an entry whose detail is empty (a task_done whose closing line arrived after the entry was
// written). An entry that already has a detail is left alone.
func FillDetail(file, id, detail string) (bool, error) {
	return update(file, id, func(e *Entry) (bool, error) {
		if e.Detail != "" || detail == "" {
			return false, nil
		}
		e.Detail = detail
		return true, nil
	})
}

// FillText sets the text of an entry whose text is empty (a task_done whose closing line arrived late). An entry that
// already has text is left alone.
func FillText(file, id, text string) (bool, error) {
	return update(file, id, func(e *Entry) (bool, error) {
		if e.Text != "" || text == "" {
			return false, nil
		}
		e.Text = text
		return true, nil
	})
}
