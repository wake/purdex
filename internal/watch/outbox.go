package watch

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"
)

// The outbox is this host's queue of messages to the watcher (spec §12.3): one message per event kind per FoldWindow (later events
// are counted into the next one), E3 never folded, and a message that could not be sent waits and is sent again as it was — the
// text is frozen when the message is made, so a retry never counts an event twice. It lives in a file, so a restart keeps it.

// FoldWindow is how long one kind of event is held to one message.
const FoldWindow = 10 * time.Minute

const maxFoldIDs = 200 // ids a folded bucket remembers (its count is exact)

// MaxMessageIDs is the most event ids one message carries; a backlog is cut into messages of this size (the receiving route accepts
// more, so the limit here is never the one that refuses).
const MaxMessageIDs = 200

// Message is one message to the watcher.
type Message struct {
	ID       string    `json:"id"`
	Backlog  bool      `json:"backlog,omitempty"`
	Host     string    `json:"host"`
	EventIDs []string  `json:"event_ids"`
	Text     string    `json:"text"`
	Created  time.Time `json:"created"`
	Attempts int       `json:"attempts,omitempty"`
	LastTry  time.Time `json:"last_try,omitempty"`
}

type foldBucket struct {
	Host  string    `json:"host"`
	Since time.Time `json:"since"` // the first event folded into it: the backlog takes every event of the kind from then on
	Count int       `json:"count"`
	IDs   []string  `json:"ids"`
}

type outboxState struct {
	Pending       []Message            `json:"pending"`
	LastSent      map[Kind]time.Time   `json:"last_sent"`
	Folded        map[Kind]*foldBucket `json:"folded"`
	LastDelivered string               `json:"last_delivered,omitempty"`
	BacklogFor    string               `json:"backlog_for,omitempty"`
}

// Outbox is the queue.
type Outbox struct {
	mu   sync.Mutex
	path string
	now  func() time.Time
	st   outboxState
	logf func(format string, args ...any)
}

// SetLogf sets where a failed write of the queue is reported (nil: nowhere). The queue goes on in memory and every change writes
// the whole file again, so a later change makes up for a write that failed.
func (o *Outbox) SetLogf(f func(format string, args ...any)) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.logf = f
}

// OpenOutbox reads the queue from path (empty: none). A file that cannot be read is kept as path+".bad" and the queue starts
// empty; the error says so, and the outbox is usable all the same.
func OpenOutbox(path string, now func() time.Time) (*Outbox, error) {
	o := &Outbox{path: path, now: now}
	o.reset()
	if path == "" {
		return o, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return o, nil
		}
		return o, err
	}
	if err := json.Unmarshal(raw, &o.st); err != nil {
		_ = os.Rename(path, path+".bad")
		o.reset()
		return o, fmt.Errorf("outbox %s unreadable, set aside as .bad: %w", path, err)
	}
	if o.st.LastSent == nil {
		o.st.LastSent = map[Kind]time.Time{}
	}
	if o.st.Folded == nil {
		o.st.Folded = map[Kind]*foldBucket{}
	}
	return o, nil
}

func (o *Outbox) reset() {
	o.st = outboxState{LastSent: map[Kind]time.Time{}, Folded: map[Kind]*foldBucket{}}
}

// save writes the state. A failure is reported, not returned: the queue goes on in memory, and the next change writes it again.
func (o *Outbox) save() {
	if o.path == "" {
		return
	}
	b, err := json.Marshal(o.st)
	if err == nil {
		_, err = writeAtomic(o.path, b)
	}
	if err != nil && o.logf != nil {
		o.logf("[watch] the event queue %s could not be written (a restart now loses what is only in memory): %v", o.path, err)
	}
}

func (o *Outbox) newMessage(host string, ids []string, text string, backlog bool) Message {
	return Message{ID: "msg-" + newID(o.now()), Backlog: backlog, Host: host, EventIDs: ids, Text: text, Created: o.now()}
}

// open says whether a message of the kind may be made now: none was, the last one is older than the window, or the clock went back.
func (o *Outbox) open(kind Kind) bool {
	last, ok := o.st.LastSent[kind]
	if !ok {
		return true
	}
	age := o.now().Sub(last)
	return age < 0 || age >= FoldWindow
}

// Add takes a recorded event: it becomes a message now (true), or is counted into the kind's folded bucket (false). E3 is always a
// message of its own.
func (o *Outbox) Add(r Record) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	defer o.save()
	if r.Kind == KindSlotOrphaned {
		o.st.Pending = append(o.st.Pending, o.newMessage(r.Host, []string{r.ID}, EventText(r), false))
		return true
	}
	if o.open(r.Kind) {
		o.st.Pending = append(o.st.Pending, o.newMessage(r.Host, []string{r.ID}, EventText(r), false))
		o.st.LastSent[r.Kind] = o.now()
		return true
	}
	b := o.st.Folded[r.Kind]
	if b == nil {
		b = &foldBucket{Host: r.Host}
		o.st.Folded[r.Kind] = b
	}
	if b.Count == 0 {
		b.Since = r.At
	}
	b.Count++
	if len(b.IDs) < maxFoldIDs {
		b.IDs = append(b.IDs, r.ID)
	}
	return false
}

func (o *Outbox) foldKinds() []Kind {
	var ks []Kind
	for k := range o.st.Folded {
		ks = append(ks, k)
	}
	sort.Slice(ks, func(i, j int) bool { return ks[i] < ks[j] })
	return ks
}

// FlushFolded turns the folded buckets whose window has ended into messages and says how many it made.
func (o *Outbox) FlushFolded() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := 0
	for _, k := range o.foldKinds() {
		if !o.open(k) {
			continue
		}
		b := o.st.Folded[k]
		o.st.Pending = append(o.st.Pending, o.newMessage(b.Host, b.IDs, FoldText(b.Host, k, b.IDs, b.Count), false))
		o.st.LastSent[k] = o.now()
		delete(o.st.Folded, k)
		n++
	}
	if n > 0 {
		o.save()
	}
	return n
}

// NextFold is when the first folded bucket's window ends.
func (o *Outbox) NextFold() (time.Time, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	var best time.Time
	found := false
	for k := range o.st.Folded {
		due := o.st.LastSent[k].Add(FoldWindow)
		if !found || due.Before(best) {
			best, found = due, true
		}
	}
	return best, found
}

// Next is the message to send first.
func (o *Outbox) Next() (Message, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.st.Pending) == 0 {
		return Message{}, false
	}
	return o.st.Pending[0], true
}

// Pending is the number of messages waiting.
func (o *Outbox) Pending() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.st.Pending)
}

// Tried notes a failed attempt to send a message; the message itself stays as it is.
func (o *Outbox) Tried(id string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for i := range o.st.Pending {
		if o.st.Pending[i].ID == id {
			o.st.Pending[i].Attempts++
			o.st.Pending[i].LastTry = o.now()
			o.save()
			return
		}
	}
}

// Delivered removes a message that was sent (the receiver said so) and moves the last delivered event id up.
func (o *Outbox) Delivered(id string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for i, m := range o.st.Pending {
		if m.ID != id {
			continue
		}
		for _, e := range m.EventIDs {
			if e > o.st.LastDelivered {
				o.st.LastDelivered = e
			}
		}
		o.st.Pending = append(o.st.Pending[:i], o.st.Pending[i+1:]...)
		o.save()
		return
	}
}

// LastDelivered is the newest event id of a message the watcher received.
func (o *Outbox) LastDelivered() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.st.LastDelivered
}

// BacklogFor is the watcher session the backlog was last made for.
func (o *Outbox) BacklogFor() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.st.BacklogFor
}

// Backlog is called when the watcher in session is one this host has not sent a backlog to. recs are this host's events that no
// watcher has dealt with; those after the last delivered one become a single summary message that replaces the queue (the queued
// messages and the folded counts are about the same events). It says whether a message was made. The session is remembered either
// way, so the summary is made once per watcher session.
func (o *Outbox) Backlog(session string, recs []Record) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if session == "" || session == o.st.BacklogFor {
		return false
	}
	o.st.BacklogFor = session
	// owed: after the watermark, or still in the queue or a folded bucket (a later event of another kind may have been delivered
	// first, so the watermark alone would drop an event that is still folded)
	queued := map[string]bool{}
	for _, m := range o.st.Pending {
		for _, id := range m.EventIDs {
			queued[id] = true
		}
	}
	var cand []Record
	for _, r := range recs {
		b := o.st.Folded[r.Kind]
		if r.ID > o.st.LastDelivered || queued[r.ID] || (b != nil && !r.At.Before(b.Since)) {
			cand = append(cand, r)
		}
	}
	made := false
	if len(cand) > 0 {
		sort.SliceStable(cand, func(i, j int) bool { return cand[i].ID < cand[j].ID })
		o.st.Pending = nil
		parts := (len(cand) + MaxMessageIDs - 1) / MaxMessageIDs
		for p := 0; p < parts; p++ {
			chunk := cand[p*MaxMessageIDs : min((p+1)*MaxMessageIDs, len(cand))]
			ids := make([]string, len(chunk))
			for i, r := range chunk {
				ids[i] = r.ID
			}
			o.st.Pending = append(o.st.Pending, o.newMessage(chunk[0].Host, ids, BacklogText(chunk[0].Host, chunk, p+1, parts), true))
		}
		o.st.Folded = map[Kind]*foldBucket{}
		made = true
	}
	o.save()
	return made
}

// Inbox is the receiving side: the ids of the events whose message this host's watcher already got, kept in a file, so a retry or a
// reply that was lost does not give the watcher the same event twice.
type Inbox struct {
	mu   sync.Mutex
	path string
	ids  []string
	seen map[string]bool
}

const inboxKeep = 2000

// OpenInbox reads the ids from path (empty: kept in memory). A file that cannot be read is set aside as path+".bad".
func OpenInbox(path string) (*Inbox, error) {
	in := &Inbox{path: path, seen: map[string]bool{}}
	if path == "" {
		return in, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return in, nil
		}
		return in, err
	}
	if err := json.Unmarshal(raw, &in.ids); err != nil {
		_ = os.Rename(path, path+".bad")
		in.ids = nil
		return in, fmt.Errorf("delivered ids %s unreadable, set aside as .bad: %w", path, err)
	}
	for _, id := range in.ids {
		in.seen[id] = true
	}
	return in, nil
}

// Seen says whether every one of ids was delivered before (a list with nothing in it is not a duplicate of anything).
func (in *Inbox) Seen(ids []string) bool {
	in.mu.Lock()
	defer in.mu.Unlock()
	if len(ids) == 0 {
		return false
	}
	for _, id := range ids {
		if !in.seen[id] {
			return false
		}
	}
	return true
}

// Mark records ids as delivered; the file keeps the latest inboxKeep.
func (in *Inbox) Mark(ids []string) error {
	in.mu.Lock()
	defer in.mu.Unlock()
	for _, id := range ids {
		if !in.seen[id] {
			in.seen[id] = true
			in.ids = append(in.ids, id)
		}
	}
	for len(in.ids) > inboxKeep {
		delete(in.seen, in.ids[0])
		in.ids = in.ids[1:]
	}
	if in.path == "" {
		return nil
	}
	b, err := json.Marshal(in.ids)
	if err != nil {
		return err
	}
	_, err = writeAtomic(in.path, b)
	return err
}
