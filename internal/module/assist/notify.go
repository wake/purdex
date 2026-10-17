package assistmod

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/wake/purdex/internal/gate"
	"github.com/wake/purdex/internal/watch"
)

// Notices and the wake of the watcher (assist plan X1b). After every poll and every switch, allow, deny and host edit the module
// works out, for each allowed entry × assist host × repo the host lists, whether it is usable, and tells the live sessions of the
// entry (on THIS host: a session elsewhere runs on its own host's gate, plan contract 3) what changed since the last pass.
// assist-notified.json keeps that last answer; this notifier is its only writer (contract 8), so a restart with nothing changed
// sends nothing.

const notifiedFile = "assist-notified.json"

// wakeBudget bounds one wake of the watcher (a start may take 3 minutes to register).
const wakeBudget = 5 * time.Minute

// maxWakeDetail is the most WakeWatcher takes in its one-line detail.
const maxWakeDetail = 500

type notifiedDoc struct {
	Usable map[string]bool `json:"usable"` // noticeKey → what that session was last told: usable
}

// noticeKey names what one session was last told: an allowed entry, a host, a repo and the session itself. Keeping the session in the
// key is what lets a member who joins later, or the session a relay moved the entry to, be told the state as it is now.
func noticeKey(kind, id, host, repo, sid string) string {
	return kind + "\x1f" + id + "\x1f" + host + "\x1f" + repo + "\x1f" + sid
}

func splitNoticeKey(k string) (kind, id, host, repo, sid string) {
	p := strings.SplitN(k, "\x1f", 5)
	for len(p) < 5 {
		p = append(p, "")
	}
	return p[0], p[1], p[2], p[3], p[4]
}

func (m *Module) readNotified() map[string]bool {
	var d notifiedDoc
	if b, err := os.ReadFile(filepath.Join(m.dir, notifiedFile)); err == nil {
		_ = json.Unmarshal(b, &d)
	}
	if d.Usable == nil {
		d.Usable = map[string]bool{}
	}
	return d.Usable
}

func (m *Module) writeNotified(state map[string]bool) error {
	b, err := json.MarshalIndent(notifiedDoc{Usable: state}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(m.dir, notifiedFile+".tmp*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(m.dir, notifiedFile))
}

// recipients are the live sessions on this host an allowed entry speaks for.
func (m *Module) recipients(kind, id string) []string {
	var out []string
	switch kind {
	case "team":
		if m.teams != nil {
			sids, err := m.teams.LocalLiveSessionsOfTeam(id)
			if err != nil {
				m.logf("[assist] sessions of team %s: %v", id, err)
			}
			out = sids
		}
	case "session":
		if o, ok := m.resolveRef(id); ok {
			out = []string{o.SessionID}
		}
	}
	live := out[:0:0]
	for _, sid := range out {
		if m.origins == nil {
			break
		}
		if _, ok, err := m.origins.ResolveOriginBySession(sid); err == nil && ok {
			live = append(live, sid)
		}
	}
	return live
}

// notifyPass is one pass of the notifier. Passes run one at a time. What a session was told is committed per session and only after
// its notice was handed over, so a failed delivery is tried again on the next pass and nobody else's state is touched.
func (m *Module) notifyPass() {
	if m.notify == nil {
		return
	}
	m.notifyMu.Lock()
	defer m.notifyMu.Unlock()
	f, err := gate.ReadAssist(m.dir)
	if err != nil {
		return
	}
	ready := m.readyCopy()
	prev := m.readNotified()
	now := m.now()

	type combo struct{ host, repo string }
	usable := map[[2]string]map[combo][2]string{} // entry → (host, repo) → {"1"|"0", why}
	for _, a := range f.Allowed {
		e := [2]string{a.Kind, a.ID}
		if usable[e] != nil {
			continue
		}
		usable[e] = map[combo][2]string{}
		for _, host := range sortedHosts(f) {
			if !gate.AllowedFor(f, host, []gate.AssistEntry{{Kind: a.Kind, ID: a.ID}}) {
				continue
			}
			repos := map[string]bool{}
			for r := range ready[host].Answer.Repos {
				repos[r] = true
			}
			for k := range prev { // a host whose check failed keeps the repos it had
				if kk, id, h, r, _ := splitNoticeKey(k); kk == a.Kind && id == a.ID && h == host {
					repos[r] = true
				}
			}
			for repo := range repos {
				ok, why := gate.Usable(f, ready, now, host, []gate.AssistEntry{{Kind: a.Kind, ID: a.ID}}, repo)
				usable[e][combo{host, repo}] = [2]string{map[bool]string{true: "1", false: "0"}[ok], why}
			}
		}
	}
	for k, was := range prev { // an entry that is gone (denied, host removed) still owes its sessions the bad news
		if !was {
			continue
		}
		kind, id, host, repo, _ := splitNoticeKey(k)
		e := [2]string{kind, id}
		if usable[e] == nil {
			usable[e] = map[combo][2]string{}
		}
		if _, in := usable[e][combo{host, repo}]; !in {
			usable[e][combo{host, repo}] = [2]string{"0", "不再允許或主機已移除"}
		}
	}

	type told struct {
		key string
		is  bool
	}
	lines := map[string][]string{} // session → one line per change
	var order []string
	changes := map[string][]told{} // session → the keys those lines settle
	unchanged := map[string]bool{} // keys of a current recipient that need no notice and stay as they are
	entries := make([][2]string, 0, len(usable))
	for e := range usable {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i][0]+entries[i][1] < entries[j][0]+entries[j][1] })
	for _, e := range entries {
		combos := make([]combo, 0, len(usable[e]))
		for c := range usable[e] {
			combos = append(combos, c)
		}
		sort.Slice(combos, func(i, j int) bool {
			return combos[i].host+"\x1f"+combos[i].repo < combos[j].host+"\x1f"+combos[j].repo
		})
		for _, sid := range m.recipients(e[0], e[1]) {
			for _, c := range combos {
				k := noticeKey(e[0], e[1], c.host, c.repo, sid)
				u := usable[e][c]
				is, was := u[0] == "1", prev[k]
				switch {
				case is && !was:
					lines[sid] = append(lines[sid], fmt.Sprintf("[輔測] %s 可用（%s）", watch.Plain(c.host), watch.Plain(c.repo)))
				case !is && was:
					lines[sid] = append(lines[sid], fmt.Sprintf("[輔測] %s 不可用（%s）：%s", watch.Plain(c.host), watch.Plain(c.repo), watch.Plain(u[1])))
				default:
					unchanged[k] = is
					continue
				}
				if _, ok := changes[sid]; !ok {
					order = append(order, sid)
				}
				changes[sid] = append(changes[sid], told{k, is})
			}
		}
	}
	state := map[string]bool{}
	for k, is := range unchanged {
		if is {
			state[k] = true
		}
	}
	for _, sid := range order {
		ls := dedupe(lines[sid])
		if err := m.notify(sid, strings.Join(ls, "\n")); err != nil {
			m.logf("[assist] notice to %s: %v (tried again at the next pass)", sid, err)
			for _, t := range changes[sid] {
				if prev[t.key] {
					state[t.key] = true // still as it was told before
				}
			}
			continue
		}
		for _, t := range changes[sid] {
			if t.is {
				state[t.key] = true
			}
		}
	}
	if err := m.writeNotified(state); err != nil {
		m.logf("[assist] write %s: %v", notifiedFile, err)
	}
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := in[:0:0]
	for _, l := range in {
		if !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	return out
}

// kickNotify asks for a pass (the notifier goroutine runs it; asking many times before it runs is one pass).
func (m *Module) kickNotify() {
	select {
	case m.nkick <- struct{}{}:
	default:
	}
}

// wakeDetail is a one-line detail for WakeWatcher: no control characters, at most 500 bytes.
func wakeDetail(s string) string {
	s = watch.Plain(s)
	if len(s) <= maxWakeDetail {
		return s
	}
	s = s[:maxWakeDetail]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// wakeItem is one waiting wake. Wakes that say the same thing about the same subject fold into the last one (a host's switch is told
// as the state it is in now); wakes about different things, such as the allows of two teams, are all kept, in order.
type wakeItem struct{ reason, detail string }

// wakeKey is what makes two wakes the same subject: a switch is about its host (the first word of the detail), an allow is about the
// entry named in the whole detail.
func wakeKey(w wakeItem) string {
	if w.reason == "assist_changed" {
		if i := strings.IndexByte(w.detail, ' '); i > 0 {
			return w.reason + "|" + w.detail[:i]
		}
	}
	return w.reason + "|" + w.detail
}

type wakeState struct {
	mu      sync.Mutex
	pending []wakeItem
	running bool
}

// wakeWatcher tells the load watcher that an assist switch or the allowed list changed. It runs in the background (a wake can take
// a start's minutes), and the work ends with the module; a failure is logged, never the caller's. The decision to start and the
// WaitGroup Add happen under the lifecycle lock Stop closes the door with, so no wake begins after Stop has started waiting.
func (m *Module) wakeWatcher(reason, detail string) {
	if m.waker == nil {
		return
	}
	item := wakeItem{reason, wakeDetail(detail)}
	m.ctxMu.Lock()
	if m.stopped {
		m.ctxMu.Unlock()
		return
	}
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	m.wake.mu.Lock()
	replaced := false
	for i, p := range m.wake.pending {
		if wakeKey(p) == wakeKey(item) {
			m.wake.pending = append(append(m.wake.pending[:i:i], m.wake.pending[i+1:]...), item)
			replaced = true
			break
		}
	}
	if !replaced {
		m.wake.pending = append(m.wake.pending, item) // not bounded: an allow is an admin's act, and every one must reach the watcher
	}
	if m.wake.running {
		m.wake.mu.Unlock()
		m.ctxMu.Unlock()
		return
	}
	m.wake.running = true
	m.wake.mu.Unlock()
	m.wakeWG.Add(1)
	m.ctxMu.Unlock()
	go func() {
		defer m.wakeWG.Done()
		for {
			m.wake.mu.Lock()
			if len(m.wake.pending) == 0 || ctx.Err() != nil {
				m.wake.running = false
				m.wake.mu.Unlock()
				return
			}
			next := m.wake.pending[0]
			m.wake.pending = m.wake.pending[1:]
			m.wake.mu.Unlock()
			wctx, cancel := context.WithTimeout(ctx, wakeBudget)
			if err := m.waker(wctx, next.reason, next.detail); err != nil && wctx.Err() == nil {
				m.logf("[assist] wake the load watcher (%s): %v", next.reason, err)
			}
			cancel()
		}
	}()
}

func onOff(on bool) string {
	if on {
		return "開"
	}
	return "關"
}
