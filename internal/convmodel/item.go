package convmodel

import "encoding/json"

// ItemType is the discriminator of an Item.
type ItemType string

// Item types.
const (
	ItemUser      ItemType = "user"
	ItemAgentText ItemType = "agent_text"
	ItemThinking  ItemType = "thinking"
	ItemStep      ItemType = "step"
	ItemSystem    ItemType = "system"
)

// Source is where a user message came from.
type Source string

// User message sources.
const (
	SourceUser      Source = "user"
	SourceQueued    Source = "queued"
	SourcePeer      Source = "peer"
	SourceSlash     Source = "slash"
	SourceBash      Source = "bash"
	SourceTask      Source = "task"
	SourceScheduled Source = "scheduled"
)

// StepKind groups tools by what they do.
type StepKind string

// Step kinds.
const (
	StepEdit    StepKind = "edit"
	StepExecute StepKind = "execute"
	StepRead    StepKind = "read"
	StepSearch  StepKind = "search"
	StepFetch   StepKind = "fetch"
	StepTask    StepKind = "task"
	StepOther   StepKind = "other"
)

// StepStatus is the state of a step.
type StepStatus string

// Step statuses.
const (
	StepRunning StepStatus = "running"
	StepDone    StepStatus = "done"
	StepFailed  StepStatus = "failed"
	StepDenied  StepStatus = "denied"
)

// SystemKind is the kind of a system item.
type SystemKind string

// System item kinds.
const (
	SystemInterrupted   SystemKind = "interrupted"
	SystemCompacted     SystemKind = "compacted"
	SystemHandoff       SystemKind = "handoff"
	SystemModelChanged  SystemKind = "model_changed"
	SystemResumed       SystemKind = "resumed"
	SystemCommandOutput SystemKind = "command_output"
	SystemNotice        SystemKind = "notice"
)

// Keep says which end of a capped step output is kept.
type Keep string

// Output ends.
const (
	KeepHead Keep = "head"
	KeepTail Keep = "tail"
)

// Item is one entry of a turn: a Type and the one non-nil variant it names.
// On the wire it is a flat object {"type": …, <the variant's fields>}.
//
// An Item decoded from an unknown type has Type set to the raw string, every
// variant nil, and re-marshals to the JSON value that was received: same
// members and values (numbers kept exactly), but compacted, not byte-identical.
type Item struct {
	Type      ItemType
	User      *UserMessage
	AgentText *AgentText
	Thinking  *Thinking
	Step      *Step
	System    *System

	// Offset is the byte offset of the row that created the item. For Go
	// callers (cursors); never on the wire.
	Offset int64

	raw json.RawMessage // the received object of an unknown type
}

// UserMessage is a prompt, from a person or from an automated source.
type UserMessage struct {
	ID          string  `json:"id"`
	At          int64   `json:"at"`
	Text        string  `json:"text"`
	Truncated   bool    `json:"truncated,omitempty"`
	Source      Source  `json:"source"`
	From        *From   `json:"from,omitempty"`
	Images      []Image `json:"images,omitempty"`
	ClientMsgID string  `json:"client_msg_id,omitempty"`
}

// From names the sender of a non-human message.
type From struct {
	Kind string `json:"kind"`
	Name string `json:"name,omitempty"`
}

// Image is an image placeholder: the decoded size, never the data.
type Image struct {
	MediaType string `json:"media_type"`
	Bytes     int64  `json:"bytes"`
}

// AgentText is a block of the agent's reply.
type AgentText struct {
	ID        string `json:"id"`
	At        int64  `json:"at"`
	Markdown  string `json:"markdown"`
	Truncated bool   `json:"truncated,omitempty"`
	Streaming bool   `json:"streaming,omitempty"`
}

// Thinking is a reasoning block; the text is often empty.
type Thinking struct {
	ID         string `json:"id"`
	At         int64  `json:"at"`
	Text       string `json:"text,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

// Step is one tool call and its result.
type Step struct {
	ID             string          `json:"id"`
	At             int64           `json:"at"`
	Kind           StepKind        `json:"kind"`
	Tool           string          `json:"tool"`
	Status         StepStatus      `json:"status"`
	Denial         string          `json:"denial,omitempty"`
	Summary        string          `json:"summary"`
	StartedAt      int64           `json:"started_at"`
	DurationMS     *int64          `json:"duration_ms,omitempty"`
	Input          json.RawMessage `json:"input"`
	InputTruncated bool            `json:"input_truncated,omitempty"`
	InputPartial   bool            `json:"input_partial,omitempty"`
	Output         *Output         `json:"output,omitempty"`
	Diff           *Diff           `json:"diff,omitempty"`
	Command        *Command        `json:"command,omitempty"`
	Subagent       *Subagent       `json:"subagent,omitempty"`
	Children       []Item          `json:"children,omitempty"`
}

// Output is a step's result text, capped. Keep is set exactly when Truncated.
type Output struct {
	Text       string  `json:"text"`
	TotalLines int     `json:"total_lines"`
	TotalBytes int     `json:"total_bytes"`
	Truncated  bool    `json:"truncated"`
	Keep       Keep    `json:"keep,omitempty"`
	Images     []Image `json:"images,omitempty"`
}

// Diff is the file change of an edit step. Exact is false when it was built
// from the tool input rather than the tool's own patch.
type Diff struct {
	Path      string `json:"path"`
	Added     int    `json:"added"`
	Removed   int    `json:"removed"`
	Exact     bool   `json:"exact"`
	Hunks     []Hunk `json:"hunks,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

// Hunk is one unified-diff hunk.
type Hunk struct {
	OldStart int      `json:"old_start"`
	OldLines int      `json:"old_lines"`
	NewStart int      `json:"new_start"`
	NewLines int      `json:"new_lines"`
	Lines    []string `json:"lines"`
}

// Command is the shell command of an execute step.
type Command struct {
	Text             string `json:"text"`
	Description      string `json:"description,omitempty"`
	ExitCode         *int   `json:"exit_code,omitempty"`
	BackgroundTaskID string `json:"background_task_id,omitempty"`
}

// Subagent links a task step to the agent it started.
type Subagent struct {
	AgentID     string `json:"agent_id"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type,omitempty"`
	Async       bool   `json:"async,omitempty"`
}

// System is a marker such as an interruption or a compaction. Detail is
// kind-specific JSON.
type System struct {
	ID     string          `json:"id"`
	At     int64           `json:"at"`
	Kind   SystemKind      `json:"kind"`
	Detail json.RawMessage `json:"detail,omitempty"`
}
