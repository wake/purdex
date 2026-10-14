package resources

import (
	"time"
	"unicode"
	"unicode/utf8"
)

// Position states (amendment A §3.1). A position is one session's place in the
// queue and, once its turn comes, its hold on the host.
const (
	PosQueued  = "queued"
	PosOffered = "offered"
	PosRunning = "running"
	PosIdle    = "idle"
	PosEnded   = "ended"
)

// Call modes: how a caller that has a session wants to be answered when the
// host is busy. They are not the settings modes (Mode*).
const (
	// CallQueue is the foreground path: answered at once, queued or granted.
	CallQueue = "queue"
	// CallAsk is the background path: waits inside the background process and
	// is asked when its turn comes.
	CallAsk = "ask"
	// CallWait is the old path (no session, or a script nobody watches): the
	// request waits until granted.
	CallWait = "wait"
	// CallAuto leaves the choice to the daemon: a caller under an interactive
	// session (one whose mod can run a prompt job, so a notice can reach it) is
	// queued, any other waits as before. It is resolved when the request is
	// created; the row keeps queue or wait.
	CallAuto = "auto"
)

// Why a queue-mode request did not run (the queued_reason column).
const (
	QueuedBehindQueue = "queue"
	QueuedOwnRunning  = "own_running"
)

// Limits of a position (amendment A V16, V19, V20).
const (
	PositionWindow      = 3 * time.Minute
	// NoticeFallbackPass is how long an undelivered offer holds the host when its
	// notice could only be told to the person (user_alert): the session cannot
	// hear it, so the offer passes early (P2f-2).
	NoticeFallbackPass = 60 * time.Second
	PositionMaxCommands = 3
	PositionMaxSpan     = 30 * time.Minute
)

// Command and cwd are stored cut to these sizes (env is never stored).
const (
	MaxCommandBytes = 200
	MaxCwdBytes     = 1024
)

// AfterEnd is what happens to a position when a command of it ends.
type AfterEnd int

const (
	// AfterIdle: the position stays, in a 3-minute window.
	AfterIdle AfterEnd = iota
	// AfterIdleWarn: as AfterIdle, and the final notice goes with the result.
	AfterIdleWarn
	// AfterRelease: the position ends.
	AfterRelease
)

// NoticeKind is why a final notice is given.
type NoticeKind string

const (
	NoticeNone          NoticeKind = ""
	NoticeOvertime      NoticeKind = "overtime"
	NoticeThreeCommands NoticeKind = "three_commands"
)

// AfterCommandEnd applies amendment A §3.3. k is the commands ended including
// this one; span is now minus the first command's start. The last result is the
// position's `warned` flag afterwards.
func AfterCommandEnd(k int, span time.Duration, warned, someoneWaiting bool) (AfterEnd, NoticeKind, bool) {
	if !someoneWaiting { // 1: no limit; an earlier notice is void
		return AfterIdle, NoticeNone, false
	}
	if warned { // 2: the announced last command has ended
		return AfterRelease, NoticeNone, true
	}
	if k+1 >= PositionMaxCommands || span >= PositionMaxSpan { // 3
		if span >= PositionMaxSpan {
			return AfterIdleWarn, NoticeOvertime, true
		}
		return AfterIdleWarn, NoticeThreeCommands, true
	}
	return AfterIdle, NoticeNone, false // 4
}

// CutCommand cuts s to at most max bytes on a rune boundary and turns control
// characters into spaces.
func CutCommand(s string, max int) string {
	out := make([]rune, 0, len(s))
	size := 0
	for _, r := range s {
		if unicode.IsControl(r) {
			r = ' '
		}
		n := utf8.RuneLen(r)
		if n < 0 {
			n = 3 // a surrogate or out-of-range rune is written as U+FFFD
		}
		if size+n > max {
			break
		}
		size += n
		out = append(out, r)
	}
	return string(out)
}
