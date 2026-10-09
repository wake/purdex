// cmd/pdx/peers_end_members_test.go
package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/wake/purdex/internal/team"
)

func TestParsePeersInvocation_EndMembersGrammar(t *testing.T) {
	for _, args := range [][]string{
		{"host", "allow-team", "a", "on", "--end-members"}, // ending members is what turning it off does
		{"host", "list", "--end-members"},
		{"host", "rename", "a", "b", "--end-members"},
		{"--end-members"},
		{"alias", "--end-members"},
	} {
		if _, _, ok := parsePeersInvocation(args); ok {
			t.Errorf("accepted %v", args)
		}
	}
	inv, _, ok := parsePeersInvocation([]string{"host", "allow-team", "air", "off", "--end-members"})
	if !ok || !inv.endMembers || inv.verb != "allow-team" {
		t.Fatalf("inv=%+v ok=%v", inv, ok)
	}
	if inv, _, ok = parsePeersInvocation([]string{"host", "allow-team", "air", "off"}); !ok || inv.endMembers {
		t.Fatalf("off alone: inv=%+v ok=%v", inv, ok)
	}
}

// fakeDaemon answers the three routes `allow-team off --end-members` uses and records the calls in order.
type fakeDaemon struct {
	mu       sync.Mutex
	calls    []string
	members  []team.RemoteMemberView
	listCode int
	endCode  map[string]int // mk → status for the end call (default 200)
}

func (d *fakeDaemon) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		defer d.mu.Unlock()
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/api/peers/hosts/air":
			d.calls = append(d.calls, "PUT hosts/air")
			_ = json.NewEncoder(w).Encode(map[string]any{"alias": "air", "host_id": "air:1", "allow_team": false, "team_roots": []string{"/r"}})
		case r.Method == http.MethodGet && r.URL.Path == team.RemoteMembersRoute:
			d.calls = append(d.calls, "GET remote-members")
			if d.listCode != 0 {
				w.WriteHeader(d.listCode)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "boom"})
				return
			}
			_ = json.NewEncoder(w).Encode(team.RemoteMembersResponse{Members: d.members})
		case r.Method == http.MethodPost && r.URL.Path == team.RemoteMembersEndRoute:
			var req team.RemoteMemberEndRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			d.calls = append(d.calls, "END "+req.MK)
			code := http.StatusOK
			if c, ok := d.endCode[req.MK]; ok {
				code = c
			}
			w.WriteHeader(code)
			switch code {
			case http.StatusOK:
				_ = json.NewEncoder(w).Encode(team.RemoteMemberEndResponse{MK: req.MK, State: "ended"})
			case http.StatusConflict:
				_ = json.NewEncoder(w).Encode(team.RemoteMemberEndError{Error: "not_live", State: "released"})
			default:
				_ = json.NewEncoder(w).Encode(team.RemoteMemberEndError{Error: "storage_error"})
			}
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

func runEndMembers(t *testing.T, d *fakeDaemon, args ...string) (int, string, string) {
	t.Helper()
	srv := httptest.NewServer(d.handler(t))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	code := runPeersCmd(append([]string{"host", "allow-team", "air", "off", "--end-members"}, append(args, "--config", cfgPath)...), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestAllowTeamOffEndMembers_EndsEveryMemberOfThatHostAfterTheSwitch(t *testing.T) {
	d := &fakeDaemon{members: []team.RemoteMemberView{
		{MK: "m1", LeadHostID: "air:1"}, {MK: "m2", LeadHostID: "other:2"}, {MK: "m3", LeadHostID: "air:1"},
	}}
	code, out, errOut := runEndMembers(t, d)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	// The consent goes off first, so no new command lands while the members are being ended.
	want := []string{"PUT hosts/air", "GET remote-members", "END m1", "END m3"}
	if !reflect.DeepEqual(d.calls, want) {
		t.Fatalf("calls = %v, want %v", d.calls, want)
	}
	if !strings.Contains(out, "ended 2 member(s)") {
		t.Fatalf("stdout = %q", out)
	}
}

func TestAllowTeamOffEndMembers_AlreadyEndedIsFine_OtherFailuresAreNot(t *testing.T) {
	d := &fakeDaemon{
		members: []team.RemoteMemberView{{MK: "m1", LeadHostID: "air:1"}, {MK: "m2", LeadHostID: "air:1"}},
		endCode: map[string]int{"m1": http.StatusConflict},
	}
	code, out, _ := runEndMembers(t, d)
	if code != 0 || !strings.Contains(out, "ended 1 member(s)") || !strings.Contains(out, "1 already ended") {
		t.Fatalf("not_live: exit %d, stdout %q", code, out)
	}

	d = &fakeDaemon{
		members: []team.RemoteMemberView{{MK: "m1", LeadHostID: "air:1"}, {MK: "m2", LeadHostID: "air:1"}},
		endCode: map[string]int{"m1": http.StatusInternalServerError},
	}
	code, _, errOut := runEndMembers(t, d)
	if code == 0 || !strings.Contains(errOut, "m1") {
		t.Fatalf("a failed end: exit %d, stderr %q", code, errOut)
	}
	// It tells the operator the switch IS off, and still tries the rest.
	if !strings.Contains(errOut, "allow-team is off") || !reflect.DeepEqual(d.calls[len(d.calls)-1], "END m2") {
		t.Fatalf("stderr %q, calls %v", errOut, d.calls)
	}
}

func TestAllowTeamOffEndMembers_ListFailureSaysTheSwitchIsOffAnyway(t *testing.T) {
	d := &fakeDaemon{listCode: http.StatusInternalServerError}
	code, _, errOut := runEndMembers(t, d)
	if code == 0 || !strings.Contains(errOut, "allow-team is off") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
}

// Without --end-members the switch alone is turned off: existing members stay (spec §5.4).
func TestAllowTeamOff_WithoutEndMembersTouchesNoMember(t *testing.T) {
	d := &fakeDaemon{members: []team.RemoteMemberView{{MK: "m1", LeadHostID: "air:1"}}}
	srv := httptest.NewServer(d.handler(t))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	if code := runPeersCmd([]string{"host", "allow-team", "air", "off", "--config", cfgPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if !reflect.DeepEqual(d.calls, []string{"PUT hosts/air"}) {
		t.Fatalf("calls = %v", d.calls)
	}
}

// `host list` shows how many remote members each lead host has here; it still lists when that count is unknown.
func TestHostList_ShowsTheRemoteMemberCount(t *testing.T) {
	hosts := []map[string]any{{"alias": "air", "host_id": "air:1"}, {"alias": "quiet", "host_id": "q:1"}}
	var failMembers bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/peers/hosts":
			_ = json.NewEncoder(w).Encode(map[string]any{"hosts": hosts})
		case team.RemoteMembersRoute:
			if failMembers {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(team.RemoteMembersResponse{Members: []team.RemoteMemberView{
				{MK: "m1", LeadHostID: "air:1"}, {MK: "m2", LeadHostID: "air:1"}}})
		}
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	cell := func(out, alias string) string {
		lines := strings.Split(strings.TrimSpace(out), "\n")
		hdr, row := strings.Fields(lines[0]), []string(nil)
		for _, l := range lines[1:] {
			if strings.HasPrefix(l, alias) {
				row = strings.Fields(l)
			}
		}
		if len(hdr) == 0 || len(row) == 0 || hdr[len(hdr)-1] != "MEMBERS" {
			t.Fatalf("header %v, row %v, want a trailing MEMBERS column", hdr, row)
		}
		return row[len(row)-1]
	}
	var stdout, stderr bytes.Buffer
	if code := runPeersCmd([]string{"host", "list", "--config", cfgPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if c := cell(stdout.String(), "air"); c != "2" {
		t.Fatalf("air members = %s, want 2\n%s", c, stdout.String())
	}
	if c := cell(stdout.String(), "quiet"); c != "0" {
		t.Fatalf("quiet members = %s, want 0", c)
	}
	failMembers = true
	stdout.Reset()
	if code := runPeersCmd([]string{"host", "list", "--config", cfgPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d with the count unavailable: %s", code, stderr.String())
	}
	if c := cell(stdout.String(), "air"); c != "-" {
		t.Fatalf("unknown count = %q, want -", c)
	}
}
