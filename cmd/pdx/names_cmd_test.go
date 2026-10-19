package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// `pdx names --json` is what the /team mod reads (name pool v2 spec §5.3): the active persona codes with their display names, and
// whether this daemon takes a lead name at all (team.lead_name.v1). Any failure is a non-zero exit, and the mod then behaves as it
// did before names existed.

type namesDaemon struct {
	caps       []string
	infoStatus int
	personas   string // the body of GET /api/hostconfig/personas
	personasSt int
	asked      atomic.Int32 // requests to the personas route
}

func (d *namesDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/api/info":
		if d.infoStatus != 0 {
			w.WriteHeader(d.infoStatus)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"capabilities": d.caps})
	case "/api/hostconfig/personas":
		d.asked.Add(1)
		if d.personasSt != 0 {
			w.WriteHeader(d.personasSt)
			return
		}
		_, _ = w.Write([]byte(d.personas))
	default:
		http.NotFound(w, r)
	}
}

const namesPersonas = `{"items":[
 {"code":"hiro","name":"Hiro Tanaka","retired":false},
 {"code":"old","name":"Old Hand","retired":true},
 {"code":"freya","name":"Freya Lindqvist","retired":false}],"stamp":{},"revision":3}`

func driveNames(t *testing.T, d http.Handler, args ...string) (int, string, string) {
	t.Helper()
	srv := httptest.NewServer(d)
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	code := runNamesCmd(context.Background(), append(args, "--config", cfgPath), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// The active personas only (a retired one is not offered), in the daemon's order, and lead_name true. Mutation gate: list the
// retired one → red; drop the capability → red.
func TestNamesCmd_JSONListsTheActivePersonasAndTheCapability(t *testing.T) {
	d := &namesDaemon{caps: []string{"personas.v1", "team.lead_name.v1"}, personas: namesPersonas}
	code, stdout, stderr := driveNames(t, d, "--json")
	if code != ExitOK {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	want := `{"personas":[{"code":"hiro","name":"Hiro Tanaka"},{"code":"freya","name":"Freya Lindqvist"}],"lead_name":true}`
	if strings.TrimSpace(stdout) != want {
		t.Fatalf("stdout = %s, want %s", stdout, want)
	}
}

// A daemon that cannot take a lead name answers an empty list and lead_name false, and the personas route is not asked at all: the
// mod needs nothing else to fall back to today's /team.
func TestNamesCmd_WithoutTheCapabilityThePersonasAreNotRead(t *testing.T) {
	d := &namesDaemon{caps: []string{"personas.v1"}, personas: namesPersonas}
	code, stdout, stderr := driveNames(t, d, "--json")
	if code != ExitOK {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	if strings.TrimSpace(stdout) != `{"personas":[],"lead_name":false}` {
		t.Fatalf("stdout = %s", stdout)
	}
	if d.asked.Load() != 0 {
		t.Fatalf("the personas route was read %d times without the capability", d.asked.Load())
	}
}

// What cannot be read is a failure, never a half answer: the mod treats any non-zero exit as "no names".
func TestNamesCmd_AFailedReadIsANonZeroExit(t *testing.T) {
	for name, d := range map[string]*namesDaemon{
		"info 500":             {infoStatus: 500},
		"personas 404":         {caps: []string{"team.lead_name.v1"}, personasSt: 404},
		"personas 500":         {caps: []string{"team.lead_name.v1"}, personasSt: 500},
		"personas not json":    {caps: []string{"team.lead_name.v1"}, personas: "nope"},
		"personas wrong shape": {caps: []string{"team.lead_name.v1"}, personas: `{"items":"x"}`},
	} {
		code, stdout, _ := driveNames(t, d, "--json")
		if code == ExitOK || strings.TrimSpace(stdout) != "" {
			t.Errorf("%s: code=%d stdout=%q, want a failure with no output", name, code, stdout)
		}
	}
}

// The text form is for a person: one line a persona. The grammar refuses anything else.
func TestNamesCmd_TextAndGrammar(t *testing.T) {
	d := &namesDaemon{caps: []string{"team.lead_name.v1"}, personas: namesPersonas}
	code, stdout, _ := driveNames(t, d)
	if code != ExitOK || !strings.Contains(stdout, "hiro") || !strings.Contains(stdout, "Hiro Tanaka") || strings.Contains(stdout, "old") {
		t.Fatalf("code=%d stdout=%q", code, stdout)
	}
	for _, args := range [][]string{{"extra"}, {"--bogus"}} {
		if code, _, _ := driveNames(t, d, args...); code != ExitUsage {
			t.Errorf("%v: code=%d, want usage", args, code)
		}
	}
}
