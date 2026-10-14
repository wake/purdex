package journal

import (
	"fmt"
	"time"
)

// Kind is the kind of a journal entry (spec §5.1).
type Kind string

const (
	KindRequest  Kind = "request"
	KindTaskNew  Kind = "task_new"
	KindTaskDone Kind = "task_done"
	KindExec     Kind = "exec"
	KindExecDone Kind = "exec_done"
	KindComm     Kind = "comm"
	KindConfirm  Kind = "confirm"
)

// By says who asked, for a request.
type By string

const (
	ByUser   By = "user"
	ByLeader By = "leader"
	ByMember By = "member"
)

var kindLabels = map[Kind]string{
	KindTaskNew:  "任務建立",
	KindTaskDone: "任務完成",
	KindExec:     "執行",
	KindExecDone: "執行完成",
	KindComm:     "溝通",
	KindConfirm:  "待確認",
}

var byLabels = map[By]string{ByUser: "使用者", ByLeader: "領導者", ByMember: "成員"}

// Item is one question of a confirm entry.
type Item struct {
	Text string `json:"text"`
	Done bool   `json:"done,omitempty"`
}

// Entry is one journal line. Its JSON form is the file's source of truth (the visible markdown is a rendering of it).
type Entry struct {
	ID     string    `json:"id"`
	Kind   Kind      `json:"kind"`
	By     By        `json:"by,omitempty"` // request only
	At     time.Time `json:"at"`
	Actor  string    `json:"actor,omitempty"`
	Person string    `json:"person,omitempty"` // the person's name, for the label of a request they make (By user); empty: 使用者
	Text   string    `json:"text,omitempty"`
	Refs   []string  `json:"refs,omitempty"`
	Items  []Item    `json:"items,omitempty"`
	Detail string    `json:"detail,omitempty"`
	Pinned bool      `json:"pinned,omitempty"`
}

// Label is the Chinese label of the kind; a request is 「<by>請求」.
func (e Entry) Label() string {
	if e.Kind == KindRequest {
		if e.By == ByUser && e.Person != "" {
			return e.Person + requestGap(e.Person) + "請求"
		}
		if l, ok := byLabels[e.By]; ok {
			return l + "請求"
		}
		return "請求"
	}
	return kindLabels[e.Kind]
}

// requestGap is the space between a person's name and 「請求」: one when the name ends in an ASCII letter or digit
// (「Wake 請求」), none after a CJK name (「王小明請求」).
func requestGap(name string) string {
	if name == "" {
		return ""
	}
	c :=name[len(name)-1]
	if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
		return " "
	}
	return ""
}

// Validate reports why an entry cannot be written.
func (e Entry) Validate() error {
	if e.ID == "" {
		return fmt.Errorf("journal: entry without id")
	}
	if e.Kind != KindRequest && kindLabels[e.Kind] == "" {
		return fmt.Errorf("journal: unknown kind %q", e.Kind)
	}
	if e.By != "" && byLabels[e.By] == "" {
		return fmt.Errorf("journal: unknown by %q", e.By)
	}
	if e.At.IsZero() {
		return fmt.Errorf("journal: entry %s without time", e.ID)
	}
	return nil
}
