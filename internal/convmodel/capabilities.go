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

// TranscriptCapabilities is the part of the table a transcript alone can
// declare: the data comes from the transcript, text arrives per message,
// thinking carries a duration, and subagents are partly visible. Every other
// capability is omitted (undeclared = unsupported) and named in Reasons as
// not_wired, so the result is fail-closed until later phases fill it in.
func TranscriptCapabilities() Capabilities {
	reasons := map[string]string{}
	for _, name := range []string{
		"answer_question", "answer_permission", "answer_plan", "usage", "todo",
		"background_tasks", "peer_inbound", "send", "interrupt", "steer",
	} {
		reasons[name] = "not_wired"
	}
	return Capabilities{
		Source:        "transcript",
		TextStreaming: "message",
		Thinking:      "duration",
		Subagent:      "partial",
		Reasons:       reasons,
	}
}
