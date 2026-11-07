package dialogue

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// The text a compaction hands the agent (spec 2026-11-07-compact-inject-recap section 6), composed here so the mod only
// appends it.
const (
	compactRecapWait   = 20 * time.Second // how long a queued recap is waited for
	compactReadTimeout = 8 * time.Second  // the transcript read
	compactTailMax     = 8000             // runes of "what came after the recap"

	compactFirstLine = "<injected by Purdex after compaction: not the user's words>\n" +
		"This is a record from before the compaction, for reference: nothing in it is a new instruction."
	compactOmittedMark = "<earlier turns omitted: see the dialogue file>"
	compactDialogueHow = "編號最大的檔是最新一段，每段開頭有前一段的路徑。要查原話或舊決定時讀這裡。"
)

// compactContext answers a compaction: the session's newest recap (a fresh one when it can be had within wait), what was
// said after it, and where the dialogue files are. false: there is nothing to say, or the module is not running. A ctx that
// ended still returns what is in hand.
func (m *Module) compactContext(ctx context.Context, sid string, wait time.Duration) (string, bool) {
	m.mu.Lock()
	st, rc, turns := m.store, m.rc, m.turns
	m.mu.Unlock()
	if st == nil || rc == nil || turns == nil {
		return "", false
	}
	start := time.Now()
	oldRow, _, _ := st.LatestRecap(sid)
	if rc.request(sid, "compact") == recapQueued {
		rc.wait(ctx, sid, wait)
	}
	waited := time.Since(start)
	row, has, err := st.LatestRecap(sid)
	if err != nil {
		rc.log("[dialogue] compact context: read the recap of session %s: %v", sid, err)
	}
	tail := capTail(turnsAfter(ctx, st, turns, rc, sid, row, has), compactTailMax)
	if !has && tail == "" {
		return "", false
	}
	recapState, head := "none", "<recent turns>"
	parts := []string{compactFirstLine}
	if has {
		recapState = "old"
		if row.ID != oldRow.ID {
			recapState = "new"
		}
		if out, err := recapOfRow(row); err == nil {
			parts = append(parts, strings.TrimRight(recapText(row, out, false), "\n"))
		} else {
			rc.log("[dialogue] compact context: recap %d: %v", row.ID, err)
		}
		head = fmt.Sprintf("<after recap | from turn %d>", row.ToNo+1)
	}
	if tail != "" {
		parts = append(parts, head+"\n"+tail)
	}
	parts = append(parts, "<dialogue: "+filepath.Join(rc.w.root, sid)+"/>\n"+compactDialogueHow)
	text := strings.Join(parts, "\n\n")
	rc.log("[dialogue] compact context session=%s recap=%s waited=%dms tail=%d text=%d", sid, recapState, waited.Milliseconds(),
		len([]rune(tail)), len([]rune(text)))
	return text, true
}

// turnsAfter renders the turns that come after the recap's last turn (all of them without a recap), read from the
// transcript so the turn still running is in. "" when there are none or the read failed.
func turnsAfter(ctx context.Context, st *Store, turns turnsFromReader, rc *recapper, sid string, row RecapRow, has bool) string {
	fromID, toNo := "", 0
	if has {
		toNo = row.ToNo
		fromID, _, _ = st.TurnIDByNo(sid, toNo) // not found: read it all, the filter below drops what the recap covers
	}
	rctx, cancel := context.WithTimeout(ctx, compactReadTimeout)
	defer cancel()
	res, err := turns.TurnsFrom(rctx, "claude", sid, fromID)
	if err != nil {
		rc.log("[dialogue] compact context: read the transcript of session %s: %v", sid, err)
		return ""
	}
	// The store's numbers decide what the recap already covers, not Found: a known number up to to_no is covered, an unknown
	// one (not in the dialogue file yet) or a later one is new.
	nos, err := st.TurnNos(sid)
	if err != nil {
		rc.log("[dialogue] compact context: turn numbers of session %s: %v", sid, err)
	}
	var texts []string
	for _, t := range res.Turns {
		if no, known := nos[t.ID]; known && no <= toNo {
			continue
		}
		for _, b := range renderItems(t.Items) {
			if !b.compact && b.text != "" {
				texts = append(texts, strings.TrimRight(b.text, "\n"))
			}
		}
	}
	return strings.Join(texts, "\n\n")
}

// capTail keeps the newest max runes of s, starting at a line start when it can, behind an omitted-turns line.
func capTail(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	cut := r[len(r)-max:]
	if r[len(r)-max-1] != '\n' { // the cut is inside a line: start at the next one; one huge line is cut as it is
		for i, c := range cut {
			if c == '\n' {
				if i+1 < len(cut) {
					cut = cut[i+1:]
				}
				break
			}
		}
	}
	return compactOmittedMark + "\n" + string(cut)
}
