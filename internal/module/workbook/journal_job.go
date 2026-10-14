package workbook

import (
	_ "embed"
)

// JournalPromptVersion is the version of the journal job's prompt (journal_ver). It is stored with the job's outbox
// row in J-1b2, not with the turn entry: the turn job and its prompt (PromptVersion 2) are untouched. A change to the
// prompt is a new version: bump it and the file name, never edit a shipped version in place.
const JournalPromptVersion = 1

// JournalPrompt is the system prompt of a journal job: the measured block of
// docs/specs/2026-10-14-workbook-journal-prompt-v3.md §1, byte for byte. It contains backticks, hence the file.
//
//go:embed prompts/journal-v1.txt
var JournalPrompt string

// JournalOutcome is a validated journal result with where it belongs: what J-1b2 stores in one transaction.
type JournalOutcome struct {
	EntryID   int64  // the turn entry the journal job followed
	ConvKey   string // the conversation
	PromptVer int    // JournalPromptVersion
	Result    JournalResult
	Usage     Usage
	LatencyMS int64
}

// journalAfter makes the journal job that follows a turn job's entry once its turn work is done, or nil when the entry
// gets none: no journal source is wired, or the session's seat is not a leader's or a solo session's (a member's turn
// writes no journal, spec §4). The seat is the one recorded on the entry when the turn was taken in.
func (e *Engine) journalAfter(j *job) *job {
	if e.d.Journal == nil {
		return nil
	}
	ent, err := e.d.Store.Entry(j.entryID)
	if err != nil {
		e.d.Logf("[workbook] journal job: %v", err)
		return nil
	}
	if _, ok := unitRoleOf(ent.Role); !ok {
		return nil
	}
	return &job{conv: j.conv, entryID: j.entryID, session: j.session, kind: JobJournal, attempt: 1, turn: j.turn}
}

// followAfterLoss is the job that still has to run when j is lost without a result (its lease ran out, or its input
// could not be built): a re-write's entry is cut and ends ok all the same, so its journal job goes on.
func followAfterLoss(j *job) *job {
	if j.kind == JobRewrite {
		return j.then
	}
	return nil
}

// buildJournal makes the call of a journal job from the unit's files as they are now. It also keeps what the answer is
// checked against on the job (jctx) - the job is leased to one stream, and Result reads it after the lease check.
func (e *Engine) buildJournal(j *job, c Complete) (Complete, map[int]int64, error) {
	ent, err := e.d.Store.Entry(j.entryID)
	if err != nil {
		return Complete{}, nil, err
	}
	role, _ := unitRoleOf(ent.Role)
	seed := JournalSeed{UnitRole: role, SelfName: e.d.Journal.SelfName(j.session)}
	files, ok, err := e.d.Journal.UnitFiles(ent)
	if err != nil {
		e.d.Logf("[workbook] journal input: unit files: %v", err) // an unreadable unit: the job runs with empty lists
	} else if ok {
		seed.Open = ReadOpenItems(files.JournalDir, e.d.Logf)
		part, _ := buildTurnPart(j.turn)
		hint := part.UserPrompt + " " + part.AssistantText + " " + recentEntryText(files.JournalDir)
		seed.Rows = ReadRoadmapRows(files.Roadmap, hint, e.d.Logf)
	}
	prompt, ctx, err := BuildJournalInput(seed, j.turn)
	if err != nil {
		return Complete{}, nil, err
	}
	j.jctx = ctx
	c.System = []SystemBlock{{Text: JournalPrompt, Cache: true}}
	c.Prompt = prompt
	c.TimeoutMS = journalTimeout
	return c, nil, nil
}

// applyJournalJob maps a journal job's result. Nothing here touches the turn entry: it is settled already, and a journal
// job that fails or times out writes nothing more for that turn (spec §9). An empty reply or text that is not the
// JSON gets the one format retry the turn job gets.
func (e *Engine) applyJournalJob(j *job, r Result) *job {
	if !r.Answered {
		if r.Reason == "empty-reply" {
			return e.journalRetry(j)
		}
		e.d.Logf("[workbook] a journal job did not answer (%s)", logKind(r.Reason))
		return nil
	}
	out, err := ParseJournalJSON(r.Text)
	if err != nil {
		return e.journalRetry(j)
	}
	e.applyJournalResult(j, ValidateJournal(out, j.jctx))
	return nil
}

func (e *Engine) journalRetry(j *job) *job {
	if j.attempt >= 2 {
		e.d.Logf("[workbook] a journal job answered in a format that is not the JSON twice; nothing is written for entry %d", j.entryID)
		return nil
	}
	retry := *j
	retry.attempt = 2
	return &retry
}

// applyJournalResult records a journal job's validated result as one outbox row (with the outbox off, nothing is
// stored) and hands it to OnJournalResult (nil in production). A store error is logged: the turn entry is settled
// already and is never touched, so the events of this turn are lost, not the entry.
func (e *Engine) applyJournalResult(j *job, res JournalResult) {
	e.d.Logf("[workbook] journal job of entry %d: %d events, %d ticks, %d roadmap changes, %d dropped",
		j.entryID, len(res.Events), len(res.Ticks), len(res.Roadmap), res.Dropped)
	out := JournalOutcome{EntryID: j.entryID, ConvKey: j.conv, PromptVer: JournalPromptVersion, Result: res, Usage: j.usage, LatencyMS: j.lat}
	if e.d.Store != nil {
		if err := e.d.Store.RecordJournalResult(out); err != nil {
			e.d.Logf("[workbook] journal job of entry %d: store the result: %v", j.entryID, err)
		}
	}
	if e.d.OnJournalResult != nil {
		e.d.OnJournalResult(out)
	}
}
