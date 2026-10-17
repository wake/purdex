package facts

import (
	"errors"
	"reflect"
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
)

func ownerSnap(procs ...ProcRec) (ProcSnapshot, ProcContext) {
	snap := ProcSnapshot{Procs: procs}
	ctx := ProcContext{Table: map[int]ProcRec{}}
	for _, p := range procs {
		ctx.Table[p.PID] = p
	}
	ctx.JobOf = func(pid int) (string, bool) {
		if pid == 900 || pid == 901 { // 900 is a wwd job's node, 901 its child
			return "j_wwd", true
		}
		return "", false
	}
	return snap, ctx
}

func TestBuildOwner(t *testing.T) {
	build := ProcRec{PID: 800, PPID: 1, UID: 501, Name: "xcodebuild"}
	sim := ProcRec{PID: 801, PPID: 1, UID: 501, Name: "SimLaunchHost.arm64"}
	svc := ProcRec{PID: 802, PPID: 1, UID: 501, Name: "XCBBuildService"}
	jobBuild := ProcRec{PID: 901, PPID: 900, UID: 501, Name: "swift-frontend"}
	jobParent := ProcRec{PID: 900, PPID: 1, UID: 501, Name: "node"}
	other := ProcRec{PID: 803, PPID: 1, UID: 501, Name: "Safari"}

	cases := []struct {
		name   string
		cfg    OwnerConfig
		idle   float64
		idleEr error
		locked bool
		procs  []ProcRec
		want   wire.OwnerState
	}{
		{"in use", OwnerConfig{}, 3.4, nil, false, nil, wire.OwnerState{Active: true, IdleS: 3, Reason: "hid"}},
		{"just under five minutes", OwnerConfig{}, 299.9, nil, false, nil, wire.OwnerState{Active: true, IdleS: 299, Reason: "hid"}},
		{"exactly five minutes is away", OwnerConfig{}, 300, nil, false, nil, wire.OwnerState{Active: false, IdleS: 300, Reason: "idle"}},
		{"away 22 minutes", OwnerConfig{}, 1332, nil, false, []ProcRec{other}, wire.OwnerState{Active: false, IdleS: 1332, Reason: "idle"}},
		{"build while away, unlocked", OwnerConfig{}, 1332, nil, false, []ProcRec{build}, wire.OwnerState{Active: true, IdleS: 1332, Reason: "build"}},
		{"build service counts", OwnerConfig{}, 1332, nil, false, []ProcRec{svc}, wire.OwnerState{Active: true, IdleS: 1332, Reason: "build"}},
		{"build while locked", OwnerConfig{}, 1332, nil, true, []ProcRec{build}, wire.OwnerState{Active: false, IdleS: 1332, Reason: "idle"}},
		{"a wwd job's own build is not the owner's", OwnerConfig{}, 1332, nil, false, []ProcRec{jobParent, jobBuild}, wire.OwnerState{Active: false, IdleS: 1332, Reason: "idle"}},
		{"simulator booted", OwnerConfig{}, 1332, nil, false, []ProcRec{sim}, wire.OwnerState{Active: true, IdleS: 1332, Reason: "simulator"}},
		{"build wins over simulator", OwnerConfig{}, 1332, nil, false, []ProcRec{sim, build}, wire.OwnerState{Active: true, IdleS: 1332, Reason: "build"}},
		{"simulator while locked", OwnerConfig{}, 1332, nil, true, []ProcRec{sim}, wire.OwnerState{Active: false, IdleS: 1332, Reason: "idle"}},
		{"configured threshold, away", OwnerConfig{IdleMinutes: 1}, 90, nil, false, nil, wire.OwnerState{Active: false, IdleS: 90, Reason: "idle"}},
		{"configured threshold, in use", OwnerConfig{IdleMinutes: 1}, 59, nil, false, nil, wire.OwnerState{Active: true, IdleS: 59, Reason: "hid"}},
		{"idle unreadable", OwnerConfig{}, 0, errors.New("no window server"), false, nil, wire.OwnerState{Active: false, IdleS: 0, Reason: "unknown"}},
		{"idle unreadable but a build runs", OwnerConfig{}, 0, errors.New("x"), false, []ProcRec{build}, wire.OwnerState{Active: true, IdleS: 0, Reason: "build"}},
	}
	for _, c := range cases {
		snap, ctx := ownerSnap(c.procs...)
		src := OwnerSources{
			IdleSeconds: func() (float64, error) { return c.idle, c.idleEr },
			Locked:      func() (bool, error) { return c.locked, nil },
		}
		got := BuildOwner(c.cfg, src, snap, ctx)
		if got == nil || !reflect.DeepEqual(*got, c.want) {
			t.Errorf("%s: got %+v want %+v", c.name, got, c.want)
		}
	}
}

func TestBuildOwnerAsksLockOnlyWhenNeeded(t *testing.T) {
	asked := 0
	src := OwnerSources{
		IdleSeconds: func() (float64, error) { return 1000, nil },
		Locked:      func() (bool, error) { asked++; return false, nil },
	}
	snap, ctx := ownerSnap(ProcRec{PID: 803, Name: "Safari"})
	BuildOwner(OwnerConfig{}, src, snap, ctx)
	if asked != 0 {
		t.Errorf("lock state read %d times with no candidate process", asked)
	}
	// in use: rule 1 decides, the lock is not needed either
	src.IdleSeconds = func() (float64, error) { return 2, nil }
	snap, ctx = ownerSnap(ProcRec{PID: 800, Name: "xcodebuild"})
	BuildOwner(OwnerConfig{}, src, snap, ctx)
	if asked != 0 {
		t.Errorf("lock state read %d times when HID already decided", asked)
	}
	// an unreadable lock counts as unlocked: the owner is not shut out of their own machine
	src.IdleSeconds = func() (float64, error) { return 1000, nil }
	src.Locked = func() (bool, error) { return false, errors.New("x") }
	if got := BuildOwner(OwnerConfig{}, src, snap, ctx); got == nil || got.Reason != "build" {
		t.Errorf("got %+v", got)
	}
}
