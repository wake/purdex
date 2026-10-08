package team

import (
	"strings"
	"testing"

	ipeers "github.com/wake/purdex/internal/peers"
)

// The down message is spelled out here rather than built from the constants,
// so rewording or re-spacing one of them is red.
func TestTaskDownMessage_Layouts(t *testing.T) {
	const report = "回報：pdx report ack|progress|ready|done --task abc123-4 --summary \"…\"（見 pdx-team skill）"
	for _, tc := range []struct {
		name string
		task Task
		want string
	}{
		{"subject only", Task{ID: "abc123-4", Subject: "Fix the build"},
			"[pdx task abc123-4] Fix the build\n\n" + report},
		{"with description", Task{ID: "abc123-4", Subject: "Fix the build", Description: "Line one.\nLine two."},
			"[pdx task abc123-4] Fix the build\n\nLine one.\nLine two.\n\n" + report},
		{"with done-when", Task{ID: "abc123-4", Subject: "Fix the build", DoneWhen: []string{"tests pass", "PR open"}},
			"[pdx task abc123-4] Fix the build\n\n完成定義：\n- tests pass\n- PR open\n\n" + report},
		{"with both", Task{ID: "abc123-4", Subject: "Fix the build", Description: "Do it.", DoneWhen: []string{"tests pass"}},
			"[pdx task abc123-4] Fix the build\n\nDo it.\n\n完成定義：\n- tests pass\n\n" + report},
		{"a brief file's trailing newlines add no blank lines", Task{ID: "abc123-4", Subject: "S", Description: "Do it.\n\n"},
			"[pdx task abc123-4] S\n\nDo it.\n\n" + report},
		{"a blank description is no description", Task{ID: "abc123-4", Subject: "S", Description: " \n\t"},
			"[pdx task abc123-4] S\n\n" + report},
	} {
		got := TaskDownMessage(tc.task)
		if got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
		for _, bad := range []string{"[pdx team]", "[pdx-relay"} {
			if strings.HasPrefix(got, bad) {
				t.Errorf("%s: starts with the reserved prefix %q", tc.name, bad)
			}
		}
		if err := ipeers.ValidateText(got); err != nil {
			t.Errorf("%s: not peer-safe: %v", tc.name, err)
		}
	}
}

// The longest message a valid task can make fits the peers limit, so the CLI
// may check the composed text instead of guessing.
func TestTaskDownMessage_WorstCaseFitsThePeersLimit(t *testing.T) {
	done := make([]string, MaxDoneWhenLines)
	for i := range done {
		done[i] = strings.Repeat("語", MaxDoneWhenLineRunes)
	}
	msg := TaskDownMessage(Task{ID: TaskWorstCaseID, Subject: strings.Repeat("語", MaxTaskSubjectRunes),
		Description: strings.Repeat("d", MaxTaskDescriptionLen), DoneWhen: done})
	if err := ipeers.ValidateText(msg); err != nil {
		t.Fatalf("the worst-case message (%d bytes) is refused: %v", len(msg), err)
	}
}
