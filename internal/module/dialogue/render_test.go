package dialogue

import (
	"encoding/json"
	"testing"

	"github.com/wake/purdex/internal/convmodel"
)

func user(src convmodel.Source, text string) convmodel.Item {
	return convmodel.Item{Type: convmodel.ItemUser, User: &convmodel.UserMessage{Source: src, Text: text}}
}

func agent(text string) convmodel.Item {
	return convmodel.Item{Type: convmodel.ItemAgentText, AgentText: &convmodel.AgentText{Markdown: text}}
}

func step(tool, summary string) convmodel.Item {
	return convmodel.Item{Type: convmodel.ItemStep, Step: &convmodel.Step{Tool: tool, Summary: summary}}
}

func compacted(detail string) convmodel.Item {
	s := &convmodel.System{Kind: convmodel.SystemCompacted}
	if detail != "" {
		s.Detail = json.RawMessage(detail)
	}
	return convmodel.Item{Type: convmodel.ItemSystem, System: s}
}

func texts(bs []block) []string {
	var out []string
	for _, b := range bs {
		if b.compact {
			out = append(out, "COMPACT:"+b.trigger)
		} else {
			out = append(out, b.text)
		}
	}
	return out
}

func TestRenderItems(t *testing.T) {
	img := user(convmodel.SourceUser, "see")
	img.User.Images = make([]convmodel.Image, 2)
	trunc := user(convmodel.SourceUser, "long")
	trunc.User.Truncated = true
	truncAgent := agent("long")
	truncAgent.AgentText.Truncated = true
	peer := user(convmodel.SourcePeer, "hello")
	peer.User.From = &convmodel.From{Kind: "peer", Name: "kenji"}
	q := step("AskUserQuestion", "ignored")
	q.Step.Question = &convmodel.StepQuestion{
		Questions: []convmodel.QuestionItem{{Question: "要不要\n進推播？"}, {Question: "b"}},
		Answers:   [][]string{{"要", "好"}, {"否"}},
	}
	unanswered := step("AskUserQuestion", "x")
	unanswered.Step.Question = &convmodel.StepQuestion{Questions: []convmodel.QuestionItem{{Question: "Q1"}}}
	withKids := step("Task", "do it")
	withKids.Step.Children = []convmodel.Item{agent("inner")}
	think := convmodel.Item{Type: convmodel.ItemThinking, Thinking: &convmodel.Thinking{Text: "hmm"}}
	notice := convmodel.Item{Type: convmodel.ItemSystem, System: &convmodel.System{Kind: convmodel.SystemNotice}}
	imgOnly := user(convmodel.SourceUser, "")
	imgOnly.User.Images = make([]convmodel.Image, 1)
	slashImg := user(convmodel.SourceSlash, "/review")
	slashImg.User.Images = make([]convmodel.Image, 1)
	taskImg := user(convmodel.SourceTask, "<summary>done</summary>")
	taskImg.User.Images = make([]convmodel.Image, 3)
	blankUser := user(convmodel.SourceUser, "  \n")

	cases := []struct {
		name  string
		items []convmodel.Item
		want  []string
	}{
		{"user", []convmodel.Item{user(convmodel.SourceUser, "hi\nthere")}, []string{"user: hi\nthere\n"}},
		{"queued", []convmodel.Item{user(convmodel.SourceQueued, "hi")}, []string{"user: hi\n"}},
		{"peer", []convmodel.Item{peer}, []string{"peer(kenji): hello\n"}},
		{"peer no name", []convmodel.Item{user(convmodel.SourcePeer, "x")}, []string{"peer(unknown): x\n"}},
		{"scheduled", []convmodel.Item{user(convmodel.SourceScheduled, "tick")}, []string{"scheduled: tick\n"}},
		{"slash first nonblank line", []convmodel.Item{user(convmodel.SourceSlash, "\n/model opus\nmore")}, []string{"<slash | /model opus>\n"}},
		{"bash first nonblank line", []convmodel.Item{user(convmodel.SourceBash, "ls -la\nx")}, []string{"<shell | ls -la>\n"}},
		{"task summary tag", []convmodel.Item{user(convmodel.SourceTask, "<task-notification>\n<summary>  Agent \"A\"\n completed </summary>\n<x>y</x>")}, []string{"<subagent done | Agent \"A\" completed>\n"}},
		{"task no tag", []convmodel.Item{user(convmodel.SourceTask, "\n\nBackground command done\nrest")}, []string{"<subagent done | Background command done>\n"}},
		{"image", []convmodel.Item{img}, []string{"user: see\n<image ×2>\n"}},
		{"image only", []convmodel.Item{imgOnly}, []string{"user:\n<image ×1>\n"}},
		{"slash with image", []convmodel.Item{slashImg}, []string{"<slash | /review>\n<image ×1>\n"}},
		{"task with image", []convmodel.Item{taskImg}, []string{"<subagent done | done>\n<image ×3>\n"}},
		{"blank user skipped", []convmodel.Item{blankUser}, nil},
		{"user truncated", []convmodel.Item{trunc}, []string{"user: long…（截斷）\n"}},
		{"agent truncated", []convmodel.Item{truncAgent}, []string{"agent: long…（截斷）\n"}},
		{"blank agent skipped", []convmodel.Item{agent("  \n "), step("Bash", "ls")}, []string{"<tool Bash | ls>\n"}},
		{"thinking and notice skipped", []convmodel.Item{think, notice}, nil},
		{"step", []convmodel.Item{step("Bash", "git status")}, []string{"<tool Bash | git status>\n"}},
		{"step children skipped", []convmodel.Item{withKids}, []string{"<tool Task | do it>\n"}},
		{"question answered multi", []convmodel.Item{q}, []string{"<tool AskUserQuestion | 要不要 進推播？>\n<answer | 要、好；否>\n"}},
		{"question unanswered", []convmodel.Item{unanswered}, []string{"<tool AskUserQuestion | Q1>\n"}},
		{"compact auto default", []convmodel.Item{compacted("")}, []string{"COMPACT:auto"}},
		{"compact manual", []convmodel.Item{compacted(`{"trigger":"manual","summary":"s"}`)}, []string{"COMPACT:manual"}},
		{"compact bad detail", []convmodel.Item{compacted(`not json`)}, []string{"COMPACT:auto"}},
		{"compact unknown trigger", []convmodel.Item{compacted(`{"trigger":"x"}`)}, []string{"COMPACT:auto"}},
		{"layout", []convmodel.Item{
			user(convmodel.SourceUser, "a"), step("Bash", "x"), step("Edit", "y"), agent("b"), user(convmodel.SourceUser, "c"),
		}, []string{"user: a\n\n<tool Bash | x>\n<tool Edit | y>\n\nagent: b\n\nuser: c\n"}},
		{"split by compact", []convmodel.Item{
			agent("a"), compacted(`{"trigger":"auto"}`), user(convmodel.SourceUser, "b"),
		}, []string{"agent: a\n", "COMPACT:auto", "user: b\n"}},
		{"empty", nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := texts(renderItems(c.items))
			if len(got) != len(c.want) {
				t.Fatalf("got %q want %q", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("block %d: got %q want %q", i, got[i], c.want[i])
				}
			}
		})
	}
}
