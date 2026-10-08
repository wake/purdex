package team

import (
	"encoding/json"
	"strings"
	"testing"
)

// The task routes' refusal codes are pinned one by one: the CLI maps them to
// exit 13 (teamRefusalCodes), so a rename is a silent cross-component break.
func TestTaskAPI_RefusalCodesArePinned(t *testing.T) {
	for name, c := range map[string]struct{ got, want string }{
		"ErrNotMember":         {ErrNotMember, "not_member"},
		"ErrTaskNotFound":      {ErrTaskNotFound, "task_not_found"},
		"ErrNotTaskOwner":      {ErrNotTaskOwner, "not_task_owner"},
		"ErrBadTaskTransition": {ErrBadTaskTransition, "bad_task_transition"},
		"ErrBlockedByUnknown":  {ErrBlockedByUnknown, "blocked_by_unknown"},
		"ErrBlockedByCycle":    {ErrBlockedByCycle, "blocked_by_cycle"},
		"ErrOwnerNotActive":    {ErrOwnerNotActive, "owner_not_active"},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", name, c.got, c.want)
		}
	}
}

// The request bodies are snake_case, origin_inbox first like KillRequest.
func TestTaskAPI_RequestsAreSnakeCase(t *testing.T) {
	for _, c := range []struct {
		v    any
		want string
	}{
		{CreateTaskRequest{OriginInbox: "i", To: "_abc123", Subject: "s", Description: "d", DoneWhen: []string{"x"}, BlockedBy: []string{"a-1"}},
			`{"origin_inbox":"i","to":"_abc123","subject":"s","description":"d","done_when":["x"],"blocked_by":["a-1"]}`},
		{TaskStatusRequest{OriginInbox: "i", Status: TaskCompleted}, `{"origin_inbox":"i","status":"completed"}`},
		{ReassignTaskRequest{OriginInbox: "i", To: "_abc123"}, `{"origin_inbox":"i","to":"_abc123"}`},
	} {
		b, err := json.Marshal(c.v)
		if err != nil || string(b) != c.want {
			t.Errorf("%T = %s (err %v), want %s", c.v, b, err, c.want)
		}
	}
}

// Responses never carry null for a list: a client iterates them as they are.
func TestTaskAPI_ResponsesNeverNullTheirLists(t *testing.T) {
	b, _ := json.Marshal(TaskList{Tasks: []Task{}})
	if string(b) != `{"tasks":[]}` {
		t.Errorf("TaskList = %s", b)
	}
	b, _ = json.Marshal(TaskDetail{Reports: []Report{}})
	if !strings.Contains(string(b), `"reports":[]`) || !strings.Contains(string(b), `"task":{`) {
		t.Errorf("TaskDetail = %s", b)
	}
}
