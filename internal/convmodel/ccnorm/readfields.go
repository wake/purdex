package ccnorm

// ReadField names one place in a transcript row that the normalizer reads.
// The golden-fixture scrubber keeps exactly these fields (plus the row's
// identity metadata) and drops the rest, so a recording can be committed
// without private data and still normalize to the same model.
type ReadField struct {
	// Row is the row type the field belongs to, or "*" for every row type
	// the normalizer reads.
	Row string
	// Path is a dotted path of keys from the row's top level; "[]" stands
	// for each element of an array ("message.content[].type").
	Path string
	// Subtree says the whole value under Path is read (a tool input, a
	// structuredPatch), so everything beneath it is kept. Without it only a
	// scalar at Path is read; an object or array is entered through the
	// longer paths of the table.
	Subtree bool
}

// ReadFields is the one table of what the normalizer reads. A scalar is kept
// only at an exact entry; an object that has no entry of its own still keeps
// its listed children, and an object or array is kept (possibly empty) so the
// count of blocks and the presence of a member do not change. Whenever a rule
// reads a new key it must be added here:
// TestReadFields_CoversDecoderKeys fails otherwise.
var ReadFields = []ReadField{
	// every row the normalizer reads
	{Row: "*", Path: "type"},
	{Row: "*", Path: "uuid"},
	{Row: "*", Path: "timestamp"},
	{Row: "*", Path: "isSidechain"},
	{Row: "*", Path: "entrypoint"},
	{Row: "*", Path: "agentId"},

	{Row: "custom-title", Path: "customTitle"},
	{Row: "ai-title", Path: "aiTitle"},

	// user rows: prompts, tool results, interrupt markers
	{Row: "user", Path: "isMeta"},
	{Row: "user", Path: "isCompactSummary"},
	{Row: "user", Path: "interruptedMessageId"},
	{Row: "user", Path: "origin"},
	{Row: "user", Path: "origin.kind"},
	{Row: "user", Path: "origin.name"},
	{Row: "user", Path: "turnOrigin"},
	{Row: "user", Path: "promptSource"},
	{Row: "user", Path: "toolDenialKind"},
	{Row: "user", Path: "toolUseResult.structuredPatch", Subtree: true},
	{Row: "user", Path: "toolUseResult.filePath"},
	{Row: "user", Path: "toolUseResult.type"},
	{Row: "user", Path: "toolUseResult.answers", Subtree: true},
	{Row: "user", Path: "toolUseResult.backgroundTaskId"},
	{Row: "user", Path: "toolUseResult.agentId"},
	{Row: "user", Path: "toolUseResult.description"},
	{Row: "user", Path: "toolUseResult.subagent_type"},
	{Row: "user", Path: "toolUseResult.isAsync"},
	{Row: "user", Path: "message.content"},
	{Row: "user", Path: "message.content[].type"},
	{Row: "user", Path: "message.content[].text"},
	{Row: "user", Path: "message.content[].source.media_type"},
	{Row: "user", Path: "message.content[].source.data"},
	{Row: "user", Path: "message.content[].tool_use_id"},
	{Row: "user", Path: "message.content[].is_error"},
	{Row: "user", Path: "message.content[].content"},
	{Row: "user", Path: "message.content[].content[].type"},
	{Row: "user", Path: "message.content[].content[].text"},
	{Row: "user", Path: "message.content[].content[].source.media_type"},
	{Row: "user", Path: "message.content[].content[].source.data"},

	// assistant rows: text, thinking, tool_use, API errors
	{Row: "assistant", Path: "thinkingDurationMs"},
	{Row: "assistant", Path: "effort"},
	{Row: "assistant", Path: "perTurnEffort"},
	{Row: "assistant", Path: "isApiErrorMessage"},
	{Row: "assistant", Path: "error"},
	{Row: "assistant", Path: "message.model"},
	{Row: "assistant", Path: "message.content"},
	{Row: "assistant", Path: "message.content[].type"},
	{Row: "assistant", Path: "message.content[].text"},
	{Row: "assistant", Path: "message.content[].thinking"},
	{Row: "assistant", Path: "message.content[].id"},
	{Row: "assistant", Path: "message.content[].name"},
	{Row: "assistant", Path: "message.content[].input", Subtree: true},

	// system rows: turn_duration, local_command, compact_boundary, informational
	{Row: "system", Path: "subtype"},
	{Row: "system", Path: "content"},
	{Row: "system", Path: "durationMs"},
	{Row: "system", Path: "level"},
	{Row: "system", Path: "compactMetadata.trigger"},

	// attachment rows: only queued_command is read
	{Row: "attachment", Path: "attachment.type"},
	{Row: "attachment", Path: "attachment.commandMode"},
	{Row: "attachment", Path: "attachment.origin"},
	{Row: "attachment", Path: "attachment.origin.kind"},
	{Row: "attachment", Path: "attachment.origin.name"},
	{Row: "attachment", Path: "attachment.prompt"},
	{Row: "attachment", Path: "attachment.prompt[].type"},
	{Row: "attachment", Path: "attachment.prompt[].text"},
	{Row: "attachment", Path: "attachment.prompt[].source.media_type"},
	{Row: "attachment", Path: "attachment.prompt[].source.data"},
}

// ReadsRow reports whether the normalizer does anything with a row of this
// type: user, assistant, custom-title and ai-title rows; the system rows
// turn_duration, local_command, compact_boundary and informational (subtype); the
// queued_command attachment (attachmentType). Every other row is counted as
// skipped and changes nothing. TestReadsRow_AgreesWithNormalizer keeps this in
// step with the row switch.
func ReadsRow(typ, subtype, attachmentType string) bool {
	switch typ {
	case "user", "assistant", "custom-title", "ai-title":
		return true
	case "system":
		return subtype == "turn_duration" || subtype == "local_command" || subtype == "compact_boundary" || subtype == "informational"
	case "attachment":
		return attachmentType == "queued_command"
	}
	return false
}
