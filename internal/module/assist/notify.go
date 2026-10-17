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
	Usable map[string]bool `json:"usable"` // "kind|id|host|repo" → was usable at the last pass
}

func noticeKey(kind, id, host, repo string) string {
	return kind + "\x1f" + id + "\x1f" + host + "\x1f" + repo
}

func splitNoticeKey(k string) (kind, id, host, repo string) {
	p := strings.SplitN(k, "\x1f", 4)
	for len(p) < 4 {
		p = append(p, "")
	}
	return p[0], p[1], p[2], p[3]
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

// notifyPass is one pass of the notifier. Passes run one at a time; the state is written after the notices are handed over.
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
	cur := map[string]bool{}
	why := map[string]string{}
	seen := map[[2]string]bool{}
	for _, a := range f.Allowed {
		if seen[[2]string{a.Kind, a.ID}] {
			continue
		}
		seen[[2]string{a.Kind, a.ID}] = true
		for _, host := range sortedHosts(f) {
			if !gate.AllowedFor(f, host, []gate.AssistEntry{{Kind: a.Kind, ID: a.ID}}) {
				continue
			}
			repos := map[string]bool{}
			for r := range ready[host].Answer.Repos {
				repos[r] = true
			}
			for k := range prev { // a host whose check failed keeps the repos it had
				if kk, id, h, r := splitNoticeKey(k); kk == a.Kind && id == a.ID && h == host {
					repos[r] = true
				}
			}
			for repo := range repos {
				ok, w := gate.Usable(f, ready, now, host, []gate.AssistEntry{{Kind: a.Kind, ID: a.ID}}, repo)
				k := noticeKey(a.Kind, a.ID, host, repo)
				cur[k], why[k] = ok, w
			}
		}
	}
	lines := map[[2]string][]string{} // entry → one line per change
	add := func(k, line string) {
		kind, id, _, _ := splitNoticeKey(k)
		e := [2]string{kind, id}
		lines[e] = append(lines[e], line)
	}
	keys := make([]string, 0, len(cur)+len(prev))
	for k := range cur {
		keys = append(keys, k)
	}
	for k, was := range prev {
		if _, in := cur[k]; !in && was {
			keys = append(keys, k)
			why[k] = "不再允許或主機已移除"
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		_, _, host, repo := splitNoticeKey(k)
		was, is := prev[k], cur[k]
		switch {
		case is && !was:
			add(k, fmt.Sprintf("[輔測] %s 可用（%s）", watch.Plain(host), watch.Plain(repo)))
		case !is && was:
			add(k, fmt.Sprintf("[輔測] %s 不可用（%s）：%s", watch.Plain(host), watch.Plain(repo), watch.Plain(why[k])))
		}
	}
	perSession := map[string][]string{}
	var order []string
	entries := make([][2]string, 0, len(lines))
	for e := range lines {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i][0]+entries[i][1] < entries[j][0]+entries[j][1] })
	for _, e := range entries {
		for _, sid := range m.recipients(e[0], e[1]) {
			if _, ok := perSession[sid]; !ok {
				order = append(order, sid)
			}
			for _, l := range lines[e] {
				dup := false
				for _, x := range perSession[sid] {
					dup = dup || x == l
				}
				if !dup {
					perSession[sid] = append(perSession[sid], l)
				}
			}
		}
	}
	for _, sid := range order {
		if err := m.notify(sid, strings.Join(perSession[sid], "\n")); err != nil {
			m.logf("[assist] notice to %s: %v", sid, err)
		}
	}
	state := map[string]bool{}
	for k, v := range cur {
		if v {
			state[k] = true
		}
	}
	if err := m.writeNotified(state); err != nil {
		m.logf("[assist] write %s: %v", notifiedFile, err)
	}
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

// wakeState is the module's one waiting wake of the watcher: wakes that come faster than one finishes are folded into the last one.
type wakeState struct {
	mu      sync.Mutex
	pending *[2]string
	running bool
}

// wakeWatcher tells the load watcher that an assist switch or the allowed list changed. It runs in the background (a wake can take
// a start's minutes), and the work ends with the module; a failure is logged, never the caller's.
func (m *Module) wakeWatcher(reason, detail string) {
	if m.waker == nil {
		return
	}
	m.ctxMu.Lock()
	ctx := m.ctx
	stopped := ctx != nil && ctx.Err() != nil
	m.ctxMu.Unlock()
	if stopped {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	m.wake.mu.Lock()
	m.wake.pending = &[2]string{reason, wakeDetail(detail)}
	if m.wake.running {
		m.wake.mu.Unlock()
		return
	}
	m.wake.running = true
	m.wake.mu.Unlock()
	m.wakeWG.Add(1)
	go func() {
		defer m.wakeWG.Done()
		for {
			m.wake.mu.Lock()
			next := m.wake.pending
			m.wake.pending = nil
			if next == nil || ctx.Err() != nil {
				m.wake.running = false
				m.wake.mu.Unlock()
				return
			}
			m.wake.mu.Unlock()
			wctx, cancel := context.WithTimeout(ctx, wakeBudget)
			if err := m.waker(wctx, next[0], next[1]); err != nil && wctx.Err() == nil {
				m.logf("[assist] wake the load watcher (%s): %v", next[0], err)
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
