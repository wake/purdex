package gate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// What `pdx gate status` says about the assist hosts (plan X2b item 4): the runs mlab has out on each host, the commands that are out,
// the holds, and for each waiter the hosts that could take it. The fleet's queue view reads it. All of it comes from the gate
// directory (assist.json, the dispatch slots, the tickets' .out files): no daemon and no ssh.

// AssistHostStatus is one assist host as the gate directory shows it.
type AssistHostStatus struct {
	Host string `json:"host"`
	On   bool   `json:"on"` // what was asked of the host (assist.json); whether it is usable is the daemon's to say
	Size int    `json:"size"`
	// Held are the runs mlab has out there now: the holders of the host's dispatch slots (their Tag is assist:<job>).
	Held  []Holder    `json:"held"`
	Holds []HoldEntry `json:"holds,omitempty"`
}

// HoldEntry is a hold that is in force: the host is left out of the walk for the repo until Until.
type HoldEntry struct {
	Host  string    `json:"host"`
	Repo  string    `json:"repo"`
	Until time.Time `json:"until"`
	Why   string    `json:"why,omitempty"`
}

// OutWaiter is a command whose ticket is out: it runs on an assist host and its ticket waits to come back or to be dropped.
type OutWaiter struct {
	Class  Class     `json:"class"`
	Ticket string    `json:"ticket"`
	PID    int       `json:"pid"` // the gate that sent it and waits for the result
	Argv   []string  `json:"argv,omitempty"`
	Cwd    string    `json:"cwd,omitempty"`
	Host   string    `json:"host"`
	Job    string    `json:"job"`
	Since  time.Time `json:"since"` // when it was sent
}

// OutWaiters lists the tickets of the pool that are out, oldest first.
func (p *Pool) OutWaiters() []OutWaiter {
	var out []OutWaiter
	for _, e := range p.peekSnapshot().Out {
		w := OutWaiter{Class: p.Class, Ticket: e.Name, Host: e.Out.Host, Job: e.Out.Job, Since: e.Out.At}
		if b, err := os.ReadFile(filepath.Join(p.queueDir(), e.Name)); err == nil {
			var info WaiterInfo
			if json.Unmarshal(b, &info) == nil {
				w.PID, w.Argv, w.Cwd = info.PID, info.Argv, info.Cwd
			}
		}
		out = append(out, w)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Since.Before(out[j].Since) })
	return out
}

// ListHolds reads the holds in force at now, by host then repo.
func ListHolds(dir string, now time.Time) []HoldEntry {
	ents, err := os.ReadDir(filepath.Join(dir, "assist-hold"))
	if err != nil {
		return nil
	}
	var out []HoldEntry
	for _, e := range ents {
		name := strings.TrimSuffix(e.Name(), ".json")
		host, repo, ok := strings.Cut(name, "@")
		if !ok || name == e.Name() {
			continue // not a hold file (a .new left by a writer, or something else)
		}
		if h, held := HeldHost(dir, host, repo, now); held {
			out = append(out, HoldEntry{Host: host, Repo: repo, Until: h.Until, Why: h.Why})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Host != out[j].Host {
			return out[i].Host < out[j].Host
		}
		return out[i].Repo < out[j].Repo
	})
	return out
}

// assistStatus fills the assist parts of a status; with no assist.json host there is nothing to add.
func (g *Gate) assistStatus(cfg Config, st *Status) {
	// The commands that are out are in the queue directory, not in assist.json: they are listed whatever the host records say (the file
	// may be unreadable for a moment, or the last host removed while a run is still out there).
	st.OutHeavy = g.pool(cfg, ClassHeavy).OutWaiters()
	st.OutLight = g.pool(cfg, ClassLight).OutWaiters()
	f, err := ReadAssist(cfg.Dir)
	if err != nil || len(f.Hosts) == 0 {
		return
	}
	now := g.Now()
	ready, _ := ReadReady(cfg.Dir)
	holds := ListHolds(cfg.Dir, now)
	names := make([]string, 0, len(f.Hosts))
	for name := range f.Hosts {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		h := f.Hosts[name]
		size := dispatchSize(h.Slots)
		hs := AssistHostStatus{Host: name, On: h.On, Size: size, Held: DispatchPool(cfg.Dir, name, size, g.Now).Held()}
		if hs.Held == nil {
			hs.Held = []Holder{}
		}
		for _, hd := range holds {
			if hd.Host == name {
				hs.Holds = append(hs.Holds, hd)
			}
		}
		st.Assist = append(st.Assist, hs)
	}
	// The hosts that could take a waiter: asked on with no switch running, a check of the current generation that is fresh and says on,
	// a free dispatch slot, and a capability that takes the command. What depends on the waiter's repo and session — the repo ready on
	// the host, the session allowed, a hold for that repo — is the daemon's and the dispatcher's to say (pdx gate hosts): status has
	// neither (a git per waiter would be too much for a polled view), so a candidate here is a host that may take it, not a promise.
	open := map[string]bool{}
	for _, name := range names {
		h := f.Hosts[name]
		r, checked := ready[name]
		fresh := checked && r.Gen == h.Gen && r.Err == "" && r.Answer.Assist && r.Answer.Switch == "on" && now.Sub(r.At) >= 0 && now.Sub(r.At) <= AssistFreshness
		free, ferr := DispatchPool(cfg.Dir, name, dispatchSize(h.Slots), g.Now).freeCapacity()
		open[name] = h.On && h.Op == nil && fresh && ferr == nil && free > 0
	}
	for _, q := range [][]Waiter{st.QueueHeavy, st.QueueLight} {
		for i := range q {
			for _, name := range names {
				if open[name] && f.Hosts[name].Takes(q[i].Argv) {
					q[i].Candidates = append(q[i].Candidates, name)
				}
			}
		}
	}
}
