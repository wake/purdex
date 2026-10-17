package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/wake/purdex/internal/gate"
)

// walkQueryTimeout is how long a gate waits for the daemon's list of usable hosts.
const walkQueryTimeout = 3 * time.Second

// gateAssistWalk is what lets a waiting `pdx gate` offer its ticket to the assist hosts (plan X2b-2). It is nil — and the gate never
// looks at an assist host — unless this host's assist.json has a host asked on, which only mlab has. The usable hosts are the daemon's
// answer for this session and directory (the same `pdx gate hosts` shows): the daemon alone knows which team a session is in. A
// daemon that cannot be asked, or does not answer, means no assist host for this look; the command waits here as it always did.
func gateAssistWalk(cfgPath, gateDir string) *gate.AssistWalk {
	f, err := gate.ReadAssist(gateDir)
	if err != nil || !anyHostOn(f) {
		return nil
	}
	sid := assistBy()
	if sid == "person" {
		sid = ""
	}
	cwd, _ := os.Getwd()
	return &gate.AssistWalk{
		Usable: func() map[string]gate.AssistHost {
			q := url.Values{"session": {sid}, "cwd": {cwd}}
			// a short wait: this is in the way of every gated command, and a daemon that is slow is a daemon with no assist host for now
			body, rc := assistCallWithin(walkQueryTimeout, cfgPath, http.MethodGet, "/api/assist/hosts?"+q.Encode(), nil, io.Discard)
			if rc != 0 {
				return nil
			}
			fresh, err := gate.ReadAssist(gateDir) // read after the answer: a row is kept only for the generation of the record it was made for
			if err != nil {
				return nil
			}
			return usableHosts(body, fresh)
		},
		Reserved: func(now time.Time) bool {
			fresh, err := gate.ReadAssist(gateDir)
			return err == nil && fresh.ReservedNow(now) != nil
		},
	}
}

func anyHostOn(f gate.AssistFile) bool {
	for _, h := range f.Hosts {
		if h.On {
			return true
		}
	}
	return false
}

// usableHosts reads the daemon's /api/assist/hosts answer and returns, by alias, the host records of those it calls usable.
func usableHosts(body []byte, f gate.AssistFile) map[string]gate.AssistHost {
	var d struct {
		Hosts []struct {
			Host   string
			Usable bool
			Gen    int
		}
	}
	if json.Unmarshal(body, &d) != nil {
		return nil
	}
	out := map[string]gate.AssistHost{}
	for _, r := range d.Hosts {
		if h, ok := f.Hosts[r.Host]; ok && r.Usable && r.Gen == h.Gen { // another generation: the record changed since the daemon looked
			out[r.Host] = h
		}
	}
	return out
}
