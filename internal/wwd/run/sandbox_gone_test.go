package run

import (
	"context"
	"testing"
)

// Gone feeds the quarantine store: only an answer that proves the unit stopped counts.
func TestSandboxGone(t *testing.T) {
	cases := []struct {
		name     string
		show     string // what `systemctl show` prints
		flag     string // a flag file the fake systemctl reacts to
		wantGone bool
		wantErr  bool
	}{
		{name: "not loaded", flag: "gone", wantGone: true},
		{name: "inactive", show: "ActiveState=inactive\n", wantGone: true},
		{name: "failed", show: "ActiveState=failed\n", wantGone: true},
		{name: "active", show: "ActiveState=active\n"},
		{name: "deactivating", show: "ActiveState=deactivating\n"},
		{name: "no state", show: "LoadState=loaded\n", wantErr: true},
		{name: "systemctl fails", flag: "show-fails", wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t)
			w.set("show", c.show)
			if c.flag != "" {
				w.set(c.flag, "")
			}
			gone, err := w.sb.Gone(context.Background(), "wwd-j1")
			if gone != c.wantGone || (err != nil) != c.wantErr {
				t.Fatalf("Gone = %v, %v; want %v, err=%v", gone, err, c.wantGone, c.wantErr)
			}
			if !w.called(t, "show wwd-j1") {
				t.Fatalf("systemctl was not asked about the unit: %v", w.calls(t))
			}
		})
	}
}

func TestSandboxGoneRefusesEmptyUnitAndCanceledContext(t *testing.T) {
	w := newWorld(t)
	if gone, err := w.sb.Gone(context.Background(), ""); gone || err == nil {
		t.Fatalf("empty unit: %v, %v", gone, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w.set("show", "ActiveState=inactive\n")
	if gone, err := w.sb.Gone(ctx, "wwd-j1"); gone || err == nil {
		t.Fatalf("canceled context must not prove anything: %v, %v", gone, err)
	}
}
