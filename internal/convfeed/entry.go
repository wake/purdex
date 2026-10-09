package convfeed

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wake/purdex/internal/convmodel"
	"github.com/wake/purdex/internal/convmodel/ccnorm"
)

// fingerprintBytes is how much of the file before the last fed offset is
// remembered to notice a same-inode truncate-and-rewrite.
const fingerprintBytes = 64

// Source is the resolver's current answer for a conversation: the open file,
// an identity that changes when the path now resolves to another file
// (device:inode), and whether a confirmed live pane runs the session.
type Source struct {
	File     File
	Identity string
	Live     bool

	// Set by the Resolver (the Entry ignores them): the pane's light or "ended" / "unknown", "terminal" while a pane
	// runs the session, and what closes the open file once the caller is done with it.
	Status  string
	Backend string
	Closer  io.Closer
	// Path is the resolved transcript path (under the symlink-resolved projects root); the Resolver sets it.
	Path string
	// StatusAt is when Status / Backend were read (before the lookup began). The Entry refuses a reading older than the
	// one it already holds, so a follower that waited for the gate cannot move the header backwards; zero = untimed,
	// always applied.
	StatusAt time.Time
	// FrameID is the confirmed owning frame of a live source (the Resolver sets it; the Entry ignores it).
	FrameID string
}

// RefreshResult says what a Refresh did to the model.
type RefreshResult struct {
	Changed bool // the revision moved (or a new epoch started)
	Reset   bool // a new epoch started: the file was replaced, shrank, was rewritten, or had a gap
}

// ErrBadCursor is returned by ParseCursor for anything that is not "<epoch>:<revision>".
var ErrBadCursor = errors.New("convfeed: bad cursor")

// Entry is one conversation: a normalizer instance fed from its transcript,
// with a revision per turn and item. mu is held for a refresh and for every
// read of the model; the caller (the cache) never holds a lock of its own
// while it runs.
type Entry struct {
	mu        sync.Mutex
	sessionID string
	// gate serializes whole resolve-and-refresh runs (Exclusive); unlike mu it can be waited on with a context.
	gate chan struct{}

	epoch    string
	norm     *ccnorm.Normalizer
	identity string
	rev      uint64
	changed  map[string]uint64 // changeKey → the revision that last changed it
	headRev  uint64            // the revision that last changed the header
	title    string
	usage    *convmodel.Usage
	live     bool
	// status and backend are the resolver's answer as of the last Refresh (the pane's light or ended / unknown, and
	// "terminal" while a pane runs the session). They live in the entry so that a header, a window and a cursor read
	// together never mix two requests' views; a change bumps the revision like a title change does.
	status   string
	backend  string
	statusAt time.Time // the reading status / backend came from

	fp    []byte // the bytes before the last fed offset
	fpEnd int64  // the offset fp ends at

	snap    convmodel.Conversation
	snapRev uint64
	snapOK  bool

	// feedHook, when set (tests only), stands in for the normalizer's Feed, to make it report a gap.
	feedHook func(off int64, line []byte) ([]ccnorm.Change, error)
}

// NewEntry returns an empty entry for the session; the first Refresh reads
// the file from zero.
func NewEntry(sessionID string) *Entry {
	e := &Entry{sessionID: sessionID, gate: make(chan struct{}, 1)}
	e.newEpoch()
	return e
}

// Exclusive runs fn with the entry's refresh gate held: one resolve-and-refresh at a time per conversation. Waiting
// for the gate ends with the context, so a request that is queued behind a long first read holds nothing (no open
// file) and goes away when its caller does.
func (e *Entry) Exclusive(ctx context.Context, fn func() error) error {
	select {
	case e.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-e.gate }()
	return fn()
}

func newEpochID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("convfeed: crypto/rand: " + err.Error()) // not reachable on a working system
	}
	return hex.EncodeToString(b[:])
}

// newEpoch starts over: a fresh normalizer and epoch, revision 0.
func (e *Entry) newEpoch() {
	e.epoch = newEpochID()
	e.norm = ccnorm.New(ccnorm.Options{SessionID: e.sessionID})
	e.rev = 0
	e.headRev = 0
	e.changed = map[string]uint64{}
	e.title, e.usage = "", nil
	e.status, e.backend, e.statusAt = "", "", time.Time{}
	e.fp, e.fpEnd = nil, 0
	e.snapOK = false
}

func turnKey(turnID string) string         { return "t\x00" + turnID }
func itemKey(turnID, itemID string) string { return "i\x00" + turnID + "\x00" + itemID }

// bump records a non-empty change list under a new revision.
func (e *Entry) bump(changes []ccnorm.Change) {
	if len(changes) == 0 {
		return
	}
	e.rev++
	for _, c := range changes {
		if c.ItemID == "" {
			e.changed[turnKey(c.TurnID)] = e.rev
		} else {
			e.changed[itemKey(c.TurnID, c.ItemID)] = e.rev
		}
	}
	e.snapOK = false
}

// Refresh brings the model up to what src's file holds now, then applies the
// liveness. A new epoch starts (and the file is read from zero) when the
// identity differs from the entry's, the file shrank below the last fed
// offset, the bytes before that offset are no longer what was read (a
// same-inode rewrite), or the normalizer reported a gap.
func (e *Entry) Refresh(ctx context.Context, src Source) (RefreshResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	var res RefreshResult
	size, err := src.File.Size()
	if err != nil {
		return res, err
	}
	if e.restart(src, size) {
		e.newEpoch()
		res.Reset = true
	}
	e.identity = src.Identity

	startRev := e.rev
	for attempt := 0; ; attempt++ {
		err = e.feedFrom(ctx, src.File, size)
		if !errors.Is(err, ccnorm.ErrGap) {
			break
		}
		if attempt > 0 {
			return res, err
		}
		e.newEpoch() // a gap: nothing can be trusted past it; read again from zero
		res.Reset = true
		e.identity = src.Identity
		startRev = 0
	}
	if err != nil {
		return res, err
	}
	// live, status and backend are one reading of the owner: a reading older than the one held changes none of them
	if stale := !src.StatusAt.IsZero() && src.StatusAt.Before(e.statusAt); !stale {
		e.bump(e.norm.SetLive(src.Live))
		e.live = src.Live
		if src.Status != e.status || src.Backend != e.backend {
			e.status, e.backend = src.Status, src.Backend
			e.rev++
			e.headRev = e.rev
		}
		if !src.StatusAt.IsZero() {
			e.statusAt = src.StatusAt // every accepted reading moves the watermark, also one that changed nothing
		}
	}
	e.refreshHeader()
	res.Changed = res.Reset || e.rev != startRev
	return res, nil
}

// restart reports whether the entry's state no longer describes src's file.
func (e *Entry) restart(src Source, size int64) bool {
	fed := e.norm.Next()
	if fed == 0 && e.identity == "" {
		return false // nothing read yet
	}
	switch {
	case e.identity != "" && src.Identity != e.identity:
		return true
	case size < fed:
		return true
	case e.fpEnd > 0 && !e.fingerprintHolds(src.File):
		return true
	}
	return false
}

func (e *Entry) fingerprintHolds(f File) bool {
	buf := make([]byte, len(e.fp))
	n, _ := f.ReadAt(buf, e.fpEnd-int64(len(e.fp))) // io.EOF with a full read is a full read
	return n == len(buf) && bytes.Equal(buf, e.fp)
}

// feedFrom reads the lines from the normalizer's next offset to size.
func (e *Entry) feedFrom(ctx context.Context, f File, size int64) error {
	_, err := readLines(ctx, f, e.norm.Next(), size, lineSink{
		feed: func(off int64, line []byte) error {
			feed := e.norm.Feed
			if e.feedHook != nil {
				feed = e.feedHook
			}
			changes, err := feed(off, line)
			if err != nil {
				return err
			}
			e.bump(changes)
			e.rollFingerprint(off, line)
			return nil
		},
		skip: func(off, length int64) error {
			if err := e.norm.Skip(off, length); err != nil {
				return err
			}
			// not read: the fingerprint stays where the last line read ends, still a valid check of the bytes before it
			return nil
		},
	})
	return err
}

// rollFingerprint keeps the last fingerprintBytes of what was actually fed, line by line, so the fingerprint is
// right at every point: a read cut short by cancellation or an error still leaves one for the offset reached,
// built from the bytes the model was built from, not from whatever the file holds by then.
func (e *Entry) rollFingerprint(off int64, line []byte) {
	end := e.norm.Next()
	if e.fpEnd != off {
		e.fp = nil // a skipped (oversize) line sits between: the bytes must be contiguous, so start again from this line
	}
	buf := make([]byte, 0, len(e.fp)+len(line)+1)
	buf = append(buf, e.fp...)
	buf = append(buf, line...)
	buf = append(buf, '\n')
	if len(buf) > fingerprintBytes {
		buf = buf[len(buf)-fingerprintBytes:]
	}
	e.fp, e.fpEnd = buf, end
}

// refreshHeader bumps the revision when the title or the usage changed.
func (e *Entry) refreshHeader() {
	title, usage := e.norm.Header()
	if title == e.title && sameUsage(usage, e.usage) {
		return
	}
	e.title, e.usage = title, usage
	e.rev++
	e.headRev = e.rev
	e.snapOK = false
}

func sameUsage(a, b *convmodel.Usage) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Model == b.Model && a.Effort == b.Effort
}

// conv is the model's copy for the current revision (cached until it moves).
// The caller holds mu.
func (e *Entry) conv() *convmodel.Conversation {
	if !e.snapOK || e.snapRev != e.rev {
		e.snap = e.norm.Conversation()
		e.snapRev = e.rev
		e.snapOK = true
	}
	return &e.snap
}

// Header is the title and usage as of the current revision.
type Header struct {
	Title   string
	Usage   *convmodel.Usage
	Live    bool
	Status  string // the pane's light, "ended" without a pane, "unknown" when the owner lookup failed
	Backend string // "terminal" while a pane runs the session
}

// Header returns the conversation-level fields.
func (e *Entry) Header() Header {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.headerLocked()
}

func (e *Entry) headerLocked() Header {
	var u *convmodel.Usage
	if e.usage != nil {
		c := *e.usage
		u = &c
	}
	return Header{Title: e.title, Usage: u, Live: e.live, Status: e.status, Backend: e.backend}
}

// Epoch is the random id of the current normalizer instance.
func (e *Entry) Epoch() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.epoch
}

// Revision is the current revision within the epoch.
func (e *Entry) Revision() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.rev
}

// HeaderChangedSince reports whether the title or usage changed after rev.
func (e *Entry) HeaderChangedSince(rev uint64) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.headRev > rev
}

// Cursor is "<epoch>:<revision>", opaque to clients.
func (e *Entry) Cursor() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cursorLocked()
}

func (e *Entry) cursorLocked() string {
	return e.epoch + ":" + strconv.FormatUint(e.rev, 10)
}

// ParseCursor splits a cursor. A foreign epoch is not an error here: the
// caller compares it with Epoch and answers a reset.
func ParseCursor(s string) (epoch string, rev uint64, err error) {
	i := strings.LastIndexByte(s, ':')
	if i <= 0 || i == len(s)-1 {
		return "", 0, fmt.Errorf("%w: %q", ErrBadCursor, s)
	}
	epoch = s[:i]
	for _, c := range epoch {
		if c < '0' || c > '9' && c < 'a' || c > 'z' {
			return "", 0, fmt.Errorf("%w: %q", ErrBadCursor, s)
		}
	}
	rev, err = strconv.ParseUint(s[i+1:], 10, 64)
	if err != nil {
		return "", 0, fmt.Errorf("%w: %q", ErrBadCursor, s)
	}
	return epoch, rev, nil
}

// TurnChange is one turn that changed after a revision: its header (Items
// empty), whether the header itself changed, and the items that changed, in
// turn order.
type TurnChange struct {
	Turn          convmodel.Turn
	HeaderChanged bool
	Items         []convmodel.Item
	// Indexes[i] is Items[i]'s 0-based position in the turn's full item list (the normalizer only appends items, so a
	// position is stable within an epoch).
	Indexes []int
}

// ChangesSince lists the turns whose header or any item changed after rev,
// oldest first, each with only its changed items (their full current state).
func (e *Entry) ChangesSince(rev uint64) []TurnChange {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.changesSinceLocked(rev)
}

// Increment is what happened after a cursor, with the header and the new cursor of the same instant. Stale: the cursor
// is not this entry's (another epoch — a restart, an eviction, a rewritten file — or a revision from the future), so
// there are no changes to give and the caller sends a fresh snapshot.
type Increment struct {
	Stale   bool
	Changes []TurnChange
	Header  Header
	Cursor  string
}

// Increment returns the changes after the cursor (epoch, rev) under one hold of the lock.
func (e *Entry) Increment(epoch string, rev uint64) Increment {
	e.mu.Lock()
	defer e.mu.Unlock()
	inc := Increment{Header: e.headerLocked(), Cursor: e.cursorLocked()}
	if epoch != e.epoch || rev > e.rev {
		inc.Stale = true
		return inc
	}
	inc.Changes = e.changesSinceLocked(rev)
	return inc
}

func (e *Entry) changesSinceLocked(rev uint64) []TurnChange {
	c := e.conv()
	var out []TurnChange
	for _, t := range c.Turns {
		var ch TurnChange
		ch.HeaderChanged = e.changed[turnKey(t.ID)] > rev
		for pos, it := range t.Items {
			if e.changed[itemKey(t.ID, ccnorm.ItemID(it))] > rev {
				ch.Items = append(ch.Items, it)
				ch.Indexes = append(ch.Indexes, pos)
			}
		}
		if !ch.HeaderChanged && len(ch.Items) == 0 {
			continue
		}
		ch.Turn = t
		ch.Turn.Items = nil
		out = append(out, ch)
	}
	return out
}
