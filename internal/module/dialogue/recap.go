package dialogue

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sync"
)

// What requestRecap answers.
const (
	recapQueued      = "queued"      // a recap is queued or running for the session (this call queued it, or one already was)
	recapUpToDate    = "up_to_date"  // no done summary is newer than the last recap
	recapUnavailable = "unavailable" // the session cannot take a job now, or the channel is stopped

	// recapInputTurns: a recap is written from the newest this many done summaries.
	recapInputTurns = 30

	recapEventType = "dialogue.recap"
)

// recapJob is a recap job's payload: the range and the summaries it is written from.
type recapJob struct {
	trigger string
	convKey string
	fromNo  int
	toNo    int
	sums    []SummaryRow // oldest first
}

// recapper decides when a recap is queued and holds the "one recap per session at a time" record.
type recapper struct {
	st   *Store
	w    *Writer
	logf func(format string, args ...any)
	// enqueue queues a job on the channel; nil until the module has built it (the request then says unavailable).
	enqueue func(sid, kind string, payload any) (id, notQueued string)
	// broadcast sends a host event; nil: nobody listens.
	broadcast func(typ, value string)

	mu       sync.Mutex
	inflight map[string]bool // sessions with a recap queued or running
}

func newRecapper(st *Store, w *Writer, logf func(format string, args ...any)) *recapper {
	return &recapper{st: st, w: w, logf: logf, inflight: map[string]bool{}}
}

func (r *recapper) log(format string, args ...any) {
	if r.logf != nil {
		r.logf(format, args...)
	}
}

func (r *recapper) release(sid string) {
	r.mu.Lock()
	delete(r.inflight, sid)
	r.mu.Unlock()
}

// request queues a recap of the session's newest done summaries unless one is already queued or running, or there is
// nothing newer than the last recap. trigger is every30 | open | manual.
func (r *recapper) request(sid, trigger string) string {
	r.mu.Lock()
	if r.inflight[sid] {
		r.mu.Unlock()
		return recapQueued
	}
	r.inflight[sid] = true
	r.mu.Unlock()

	payload, why := r.plan(sid, trigger)
	if why != "" {
		r.release(sid)
		return why
	}
	if r.enqueue == nil {
		r.release(sid)
		return recapUnavailable
	}
	if _, notQueued := r.enqueue(sid, kindRecap, payload); notQueued != "" {
		r.release(sid)
		return recapUnavailable
	}
	return recapQueued
}

// plan reads the summaries for the job; why is set when there is no job to queue.
func (r *recapper) plan(sid, trigger string) (recapJob, string) {
	last, err := r.st.LatestRecapToNo(sid)
	if err != nil {
		r.log("[dialogue] recap of session %s: %v", sid, err)
		return recapJob{}, recapUnavailable
	}
	fresh, err := r.st.SummariesRange(sid, last+1, 1<<30, 1)
	if err != nil {
		r.log("[dialogue] recap of session %s: %v", sid, err)
		return recapJob{}, recapUnavailable
	}
	if len(fresh) == 0 {
		return recapJob{}, recapUpToDate
	}
	sums, err := r.st.SummariesRange(sid, 0, 1<<30, recapInputTurns)
	if err != nil || len(sums) == 0 {
		r.log("[dialogue] recap of session %s: read summaries: %v", sid, err)
		return recapJob{}, recapUnavailable
	}
	newest := sums[len(sums)-1]
	return recapJob{trigger: trigger, convKey: newest.ConvKey, fromNo: sums[0].TurnNo, toNo: newest.TurnNo, sums: sums}, ""
}

// recapKind is the recap job kind (kindHandler).
type recapKind struct{ r *recapper }

var _ kindHandler = (*recapKind)(nil)

func recapPayload(j job) (recapJob, error) {
	p, ok := j.payload.(recapJob)
	if !ok {
		return recapJob{}, fmt.Errorf("dialogue: a recap job with payload %T", j.payload)
	}
	return p, nil
}

func (k *recapKind) build(j job) (callInput, error) {
	p, err := recapPayload(j)
	if err != nil {
		return callInput{}, err
	}
	type turn struct {
		N       int    `json:"n"`
		At      string `json:"at"`
		Title   string `json:"title"`
		Summary string `json:"summary"`
	}
	in := struct {
		Turns []turn `json:"turns"`
	}{Turns: []turn{}}
	for _, s := range p.sums {
		in.Turns = append(in.Turns, turn{N: s.TurnNo, At: localHM(s.TurnAt), Title: s.Title, Summary: s.Summary})
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(in); err != nil {
		return callInput{}, err
	}
	return callInput{System: recapPrompt, Prompt: string(bytes.TrimRight(buf.Bytes(), "\n"))}, nil
}

// apply stores the answer: one row per job id, so a second call for the same answer changes nothing.
func (k *recapKind) apply(j job, text string, u usage) error {
	p, err := recapPayload(j)
	if err != nil {
		return err
	}
	out, err := parseRecap(text)
	if err != nil {
		return err // wraps errFormat
	}
	out = repairRecap(out)
	body, err := json.Marshal(out)
	if err != nil {
		return err
	}
	_, _, err = k.r.st.InsertRecap(RecapRow{JobID: j.id, SessionID: j.sid, ConvKey: p.convKey, FromNo: p.fromNo, ToNo: p.toNo,
		Trigger: p.trigger, Headline: out.Headline, BodyJSON: string(body), UsageIn: u.In, UsageOut: u.Out, LatencyMS: u.LatencyMS})
	return err
}

// after runs once per stored answer: the recap goes into the dialogue file and the host is told. Best-effort.
func (k *recapKind) after(j job) {
	r := k.r
	defer r.release(j.sid)
	row, found, err := r.st.RecapByJob(j.id)
	if err != nil || !found {
		r.log("[dialogue] read back recap of job %s: found=%v err=%v", j.id, found, err)
		return
	}
	var out recapOut
	if err := json.Unmarshal([]byte(row.BodyJSON), &out); err != nil {
		r.log("[dialogue] recap %d: %v", row.ID, err)
		return
	}
	if err := r.w.AppendMarker(row.SessionID, row.ToNo, recapText(row, out, false)); err != nil {
		r.log("[dialogue] put recap %d into the dialogue file: %v", row.ID, err)
	}
	if r.broadcast != nil {
		v, _ := json.Marshal(struct {
			SessionID string `json:"session_id"`
			RecapID   int64  `json:"recap_id"`
		}{row.SessionID, row.ID})
		r.broadcast(recapEventType, string(v))
	}
}

// finish: a recap has no row before it is stored, so it only frees the session's slot for the next trigger.
func (k *recapKind) finish(j job, state, reason string) { k.r.release(j.sid) }

// recapOfRow is the structured body of a stored recap.
func recapOfRow(row RecapRow) (recapOut, error) {
	var out recapOut
	err := json.Unmarshal([]byte(row.BodyJSON), &out)
	return out, err
}

// carriedRecap is the text a new segment starts with: the session's newest recap whose to_no is at most beforeNo, under
// "<carried recap …>"; "" when there is none.
func carriedRecap(st *Store, sid string, beforeNo int) string {
	row, found, err := st.LatestRecapUpTo(sid, beforeNo)
	if err != nil || !found {
		return ""
	}
	out, err := recapOfRow(row)
	if err != nil {
		return ""
	}
	return recapText(row, out, true)
}
