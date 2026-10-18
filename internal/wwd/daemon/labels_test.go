package daemon

import (
	"slices"
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
)

func str(v string) *string { return &v }

func infoOf(t *testing.T, d *Daemon) wire.Info {
	t.Helper()
	w := do(d.TCPHandler(), "GET", "/v1/info", "Bearer "+testToken)
	if w.Code != 200 {
		t.Fatalf("code %d body %s", w.Code, w.Body)
	}
	info, err := wire.Decode[wire.Info](w.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func labelsEvent(t *testing.T, e wire.Event) wire.LabelsEvent {
	t.Helper()
	if e.Type != wire.EventLabels {
		t.Fatalf("event %s, want labels", e.Type)
	}
	ev, err := wire.Decode[wire.LabelsEvent](e.Data)
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

// The labels and the fingerprint in /v1/info follow the facts; a labels event
// goes out only when the labels really changed.
func TestInfoLabelsFollowFactsAndEventsOnlyOnChange(t *testing.T) {
	d := newTestDaemon(t)
	ch, cancel := d.Events().Subscribe(false)
	defer cancel()

	// before any record: the platform the daemon was built for, and an empty fingerprint
	if info := infoOf(t, d); len(info.Labels) < 2 || info.Fingerprint == nil || len(info.Fingerprint) != 0 {
		t.Fatalf("before the first record: %+v", info)
	}

	f := wire.Facts{Platform: "linux", Arch: "amd64", OSVersion: "Ubuntu 26.04", Tools: wire.Tools{Node: str("24.14.0")}}
	d.OnFacts(f)
	if e := recv(t, ch); e.Type != wire.EventFacts {
		t.Fatalf("first the facts, got %s", e.Type)
	}
	ev := labelsEvent(t, recv(t, ch))
	want := []string{"arch:amd64", "os:linux", "tool:node24"}
	if ev.Host != "testhost" || !slices.Equal(ev.Labels, want) {
		t.Fatalf("event %+v", ev)
	}
	info := infoOf(t, d)
	if !slices.Equal(info.Labels, want) || info.Fingerprint["node"] != "24.14.0" || info.Fingerprint["os_version"] != "Ubuntu 26.04" {
		t.Fatalf("info %+v", info)
	}

	// the same labels again (a patch bump moves the fingerprint only): facts, no labels event
	f.Tools.Node = str("24.15.0")
	d.OnFacts(f)
	if e := recv(t, ch); e.Type != wire.EventFacts {
		t.Fatalf("got %s", e.Type)
	}
	none(t, ch)
	if got := infoOf(t, d).Fingerprint["node"]; got != "24.15.0" {
		t.Fatalf("fingerprint did not follow: %q", got)
	}
}

// A stream that opens (or reopens) after the labels were published starts with
// the current facts and then the current labels: it does not have to wait for a
// change that may never come.
func TestLateSubscriberGetsTheCurrentLabels(t *testing.T) {
	d := newTestDaemon(t)
	d.OnFacts(wire.Facts{Platform: "linux", Arch: "amd64", Tools: wire.Tools{Node: str("24.14.0")}})
	d.OnFacts(wire.Facts{Platform: "linux", Arch: "amd64", Tools: wire.Tools{Node: str("24.14.0")}}) // no change, no event
	ch, cancel := d.Events().Subscribe(false)
	defer cancel()
	if e := recv(t, ch); e.Type != wire.EventFacts {
		t.Fatalf("first the facts, got %s", e.Type)
	}
	ev := labelsEvent(t, recv(t, ch))
	if !slices.Equal(ev.Labels, []string{"arch:amd64", "os:linux", "tool:node24"}) {
		t.Fatalf("%v", ev.Labels)
	}
	none(t, ch)
	// a change after that reaches the open stream
	d.OnFacts(wire.Facts{Platform: "linux", Arch: "amd64", Tools: wire.Tools{Node: str("25.0.0")}})
	recv(t, ch) // facts
	if ev := labelsEvent(t, recv(t, ch)); !slices.Contains(ev.Labels, "tool:node25") {
		t.Fatalf("%v", ev.Labels)
	}
}

// RepoSource is the hook for the prepared-clone registry: its repos become
// repo:<name> labels.
func TestRepoSourceLabels(t *testing.T) {
	d := New(Options{
		HostID: "h1", Alias: "testhost", Version: "v0", Token: testToken,
		RepoSource: func() []string { return []string{"purdex", "mlab"} },
	})
	d.OnFacts(wire.Facts{Platform: "darwin", Arch: "arm64"})
	want := []string{"arch:arm64", "os:darwin", "repo:mlab", "repo:purdex"}
	if got := infoOf(t, d).Labels; !slices.Equal(got, want) {
		t.Fatalf("labels %v", got)
	}
}
