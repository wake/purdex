package convmodel

// Capabilities is the graded, fail-closed capability table of a
// conversation ([D §13]). A capability that is not declared is unsupported
// (send and interrupt have no "none"); Reasons names why, per capability.
// A missing Capabilities object — not an incomplete one — means "loading".
type Capabilities struct {
	Source           string `json:"source,omitempty"`
	TextStreaming    string `json:"text_streaming,omitempty"`
	Thinking         string `json:"thinking,omitempty"`
	AnswerQuestion   string `json:"answer_question,omitempty"`
	AnswerPermission string `json:"answer_permission,omitempty"`
	AnswerPlan       string `json:"answer_plan,omitempty"`
	Usage            string `json:"usage,omitempty"`
	Todo             string `json:"todo,omitempty"`
	Subagent         string `json:"subagent,omitempty"`
	BackgroundTasks  string `json:"background_tasks,omitempty"`
	PeerInbound      string `json:"peer_inbound,omitempty"`
	Send             string `json:"send,omitempty"`
	Interrupt        string `json:"interrupt,omitempty"`
	Steer            string `json:"steer,omitempty"`

	Reasons map[string]string `json:"reasons,omitempty"`
}
