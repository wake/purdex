package ccnorm

import (
	"bufio"
	"io"
	"regexp"

	"github.com/wake/purdex/internal/convmodel"
)

// NormalizeSubagent turns a subagent's own transcript file
// (`<sid>/subagents/agent-<id>.jsonl`, every row sidechain) into the items of
// one pseudo-turn — what a parent step's `children` hold. The row rules are
// the main file's, with sidechain rows accepted, rows that name another agent
// left out, and the first prompt row (the brief the parent gave) taken as
// `user{source: task}` (lead ruling D7).
//
// The file is read as live: a tool call with no result yet is `running`. The
// turns of the file, if it has several (an agent that was messaged again), are
// flattened in order. Lines over the line cap are skipped and counted, never
// an error; a read error ends the file.
func NormalizeSubagent(r io.Reader, agentID string) ([]convmodel.Item, Stats) {
	n := New(Options{})
	n.sub, n.subAgent = true, agentID

	br := bufio.NewReaderSize(r, 64<<10)
	var buf []byte
	var size int64 // bytes of the current line so far, newline included
	over := false  // the current line is over the cap: stop keeping it
	for {
		chunk, err := br.ReadSlice('\n')
		size += int64(len(chunk))
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
			line := size
			if len(chunk) > 0 && chunk[len(chunk)-1] == '\n' {
				line-- // not part of the line
			}
			if over {
				n.skipOversize(line)
			} else {
				_, _ = n.Feed(n.Next(), buf[:line])
			}
		}
		buf, size, over = buf[:0], 0, false
		if err != nil {
			break
		}
	}

	items := []convmodel.Item{}
	for _, t := range n.Conversation().Turns {
		items = append(items, t.Items...)
	}
	return items, n.Stats()
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
