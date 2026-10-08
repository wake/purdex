package ccnorm

import (
	"fmt"
	"math/rand/v2"
	"strings"
)

// toolGen adds tool calls and results to a generated transcript: every tool
// kind, results that succeed, fail, are denied, never come, come late (after
// their turn) or come for nothing, results in pairs, large inputs and outputs,
// images, patches and agent ids. It only writes fields and spellings from
// spec §3 M-U1-7.
type toolGen struct {
	r    *rand.Rand
	late []string // tool_use ids still waiting for a result
}

func (g *toolGen) emit(id func(string) string, tick func() float64, model string, ep opt, add func([]byte)) {
	r := g.r
	call := func(name string, input obj) string {
		tu := id("toolu")
		add(assistantRow(id("a"), tick(), model, toolUseBlock(tu, name, input), ep))
		return tu
	}
	switch r.IntN(14) {
	case 0: // a result for an earlier call, usually in another turn
		if len(g.late) > 0 {
			i := r.IntN(len(g.late))
			add(resultRow(id("r"), tick(), g.late[i], "late result", false, ep))
			g.late = append(g.late[:i], g.late[i+1:]...)
			return
		}
		fallthrough
	case 1: // a result for nothing
		add(resultRow(id("r"), tick(), id("toolu-gone"), "orphan", false, ep))
	case 2:
		tu := call("Bash", obj{"command": "ls\nsecond line"})
		add(resultRow(id("r"), tick(), tu, "Exit code 1\nboom", true, ep))
	case 3:
		tu := call("Edit", obj{"file_path": "/work/x/a.go", "old_string": "a\nb", "new_string": "c"})
		add(resultRow(id("r"), tick(), tu, "updated", false, ep, toolUseResult(obj{"filePath": "/work/x/a.go",
			"structuredPatch": []obj{hunkObj(1, 2, 1, 1, "-a", "-b", "+c")}})))
	case 4:
		tu := call("Edit", obj{"file_path": "/work/x/a.go", "old_string": "a", "new_string": "b"})
		add(resultRow(id("r"), tick(), tu, refusal, true, ep, denialKind([]string{"user-rejected", "permission-rule", "interrupted", "cancelled"}[r.IntN(4)])))
	case 5: // never answered: the turn may end first
		g.late = append(g.late, call("Write", obj{"file_path": "/work/x/big.txt", "content": strings.Repeat("line\n", 1000)}))
	case 6:
		tu := call("Read", obj{"file_path": "/work/x/pic.png"})
		add(resultRow(id("r"), tick(), tu, []obj{imgObj("image/png", "QUJDREVG"), {"type": "text", "text": "caption"}}, false, ep))
	case 7:
		tu := call("Agent", obj{"description": "look", "subagent_type": "Explore", "prompt": strings.Repeat("p", 6000)})
		add(resultRow(id("r"), tick(), tu, "found", false, ep, toolUseResult(obj{"status": "completed", "agentId": "a" + id("ag"), "isAsync": r.IntN(2) == 0})))
	case 8: // a big output, cut at 16 KiB
		name := []string{"Bash", "Read"}[r.IntN(2)] // the tail for execute, the head otherwise
		tu := call(name, obj{"command": "yes | head -n 12000", "file_path": "/work/x/log.txt"})
		add(resultRow(id("r"), tick(), tu, strings.Repeat("y\n", 12000), false, ep))
	case 9: // two results in one row
		t1 := call("Grep", obj{"pattern": "x"})
		t2 := call("Glob", obj{"pattern": "*.go"})
		o := common("user", id("r"), tick())
		o["message"] = obj{"role": "user", "content": []obj{
			{"type": "tool_result", "tool_use_id": t1, "content": "hit"},
			{"type": "tool_result", "tool_use_id": t2, "content": "a.go", "is_error": true},
		}}
		add(line(o, ep, denialKind("user-rejected")))
	case 10:
		tu := call("MultiEdit", obj{"file_path": "/f", "edits": []obj{{"old_string": "a", "new_string": "b"}, {"old_string": strings.Repeat("x\n", 300), "new_string": strings.Repeat("y\n", 300)}}})
		add(resultRow(id("r"), tick(), tu, "ok", false, ep))
	case 11: // a whole input over 16 KiB made of small strings
		in := obj{}
		for i := range 30 {
			in[fmt.Sprintf("k%02d", i)] = strings.Repeat("v", 900)
		}
		tu := call("mcp__srv__tool", in)
		add(resultRow(id("r"), tick(), tu, `{"ok":true}`, false, ep))
	case 12:
		tu := call("AskUserQuestion", obj{"questions": []obj{{"question": "A or B?", "header": "h", "options": []obj{{"label": "A"}, {"label": "B"}}}}})
		add(resultRow(id("r"), tick(), tu, "answered", false, ep))
	default:
		tu := call("Bash", obj{"command": "echo ok", "run_in_background": r.IntN(2) == 0})
		add(resultRow(id("r"), tick(), tu, "ok", false, ep, toolUseResult(obj{"stdout": "ok", "backgroundTaskId": "bg1"})))
	}
}
