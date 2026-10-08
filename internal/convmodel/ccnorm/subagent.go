package ccnorm

import (
	"bufio"
	"fmt"
	"io"
	"regexp"

	"github.com/wake/purdex/internal/convmodel"
)

// Totals over one subagent file. The census (14 days) tops out at 1,622 rows
// in a file, 42 files over 8 MB. Variables only so tests can lower them.
var (
	maxSubagentBytes int64 = 64 << 20 // bytes read in total
	maxSubagentLines       = 50_000   // lines read in total
	maxSubagentItems       = 20_000   // items kept
)

// NormalizeSubagent turns a subagent's own transcript file
// (`<sid>/subagents/agent-<id>.jsonl`, every row sidechain) into the items of
// one pseudo-turn — what a parent step's `children` hold. The row rules are
// the main file's, with sidechain rows accepted, rows that name another agent
// or none left out (when agentID is not empty), and the first prompt row (the
// brief the parent gave) taken as `user{source: task}` (lead ruling D7).
//
// The file is read as live: a tool call with no result yet is `running`. The
// turns of the file, if it has several (an agent that was messaged again), are
// flattened in order. Lines over the line cap are skipped and counted, never
// an error.
//
// Totals are capped (64 MiB, 50,000 lines, 20,000 items): past a cap reading
// stops, Skipped["subagent:truncated"] is set and what was built is returned.
// The error is non-nil only when the reader fails with something other than
// io.EOF; the items and Stats read up to then are returned with it.
func NormalizeSubagent(r io.Reader, agentID string) ([]convmodel.Item, Stats, error) {
	n := New(Options{})
	n.sub, n.subAgent = true, agentID

	br := bufio.NewReaderSize(r, 64<<10)
	var buf []byte
	var size int64  // bytes of the current line so far, newline included
	var total int64 // bytes read so far
	over := false   // the current line is over the cap: stop keeping it
	truncated := false
	var readErr error
	for {
		chunk, err := br.ReadSlice('\n')
		size += int64(len(chunk))
		total += int64(len(chunk))
		if total > maxSubagentBytes {
			truncated = true
			break
		}
		if !over {
			if len(buf)+len(chunk) > maxLineBytes+1 {
				over, buf = true, nil
			} else {
				buf = append(buf, chunk...)
			}
		}
		if err == bufio.ErrBufferFull {
			continue // the line goes on
		}
		if size > 0 {
			if n.stats.Lines >= maxSubagentLines {
				truncated = true
				break
			}
			line := size
			if len(chunk) > 0 && chunk[len(chunk)-1] == '\n' {
				line-- // not part of the line
			}
			if over {
				n.skipOversize(line)
			} else {
				_, _ = n.Feed(n.Next(), buf[:line])
			}
			if len(n.itemAt) > maxSubagentItems {
				truncated = true
				break
			}
		}
		buf, size, over = buf[:0], 0, false
		if err != nil {
			if err != io.EOF {
				readErr = fmt.Errorf("ccnorm: read subagent transcript: %w", err)
			}
			break
		}
	}
	if truncated {
		n.skip("subagent:truncated")
	}

	// Flattened, and bounded by the item cap: no second copy of an
	// unbounded file.
	items := []convmodel.Item{}
flatten:
	for _, t := range n.Conversation().Turns {
		for _, it := range t.Items {
			if len(items) >= maxSubagentItems {
				break flatten
			}
			items = append(items, it)
		}
	}
	return items, n.Stats(), readErr
}

// agentIDRe is what an agent id may look like: it names a file
// (`subagents/agent-<id>.jsonl`), so nothing that could walk a path gets in.
var agentIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

// subagentOf links a task step to the agent it started: toolUseResult.agentId
// and isAsync, with the step's own description and subagent_type. nil when
// the result names no (valid) agent.
func (n *Normalizer) subagentOf(s *convmodel.Step, tur object) *convmodel.Subagent {
	id := tur.str("agentId")
	if id == "" {
		return nil
	}
	if !agentIDRe.MatchString(id) {
		n.skip("subagent:bad_id")
		return nil
	}
	in, _ := parseObject(s.Input)
	desc := in.str("description")
	if desc == "" {
		desc = tur.str("description")
	}
	typ := in.str("subagent_type")
	if typ == "" {
		typ = tur.str("subagent_type")
	}
	desc, _ = capText(desc, convmodel.MaxInputString)
	typ, _ = capText(typ, maxDenial)
	return &convmodel.Subagent{AgentID: id, Description: desc, Type: typ, Async: jsonTrue(tur.get("isAsync"))}
}
