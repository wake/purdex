package daemon

import (
	"reflect"
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
)

func holdExc() wire.Exception {
	e := exc("release-hold", 2*3600e9, wire.HoldHeavy, wire.HoldLight)
	e.Allow = &wire.HoldAllow{Classes: []wire.JobClass{wire.ClassRelease}, Repos: []string{"purdex"}}
	e.Flags = []wire.HoldFlag{wire.HoldFlagRelease}
	return e
}

// A hold survives a restart with its fields (the reason 8b-3 needed schema 7): a wwd that forgot allow would turn a selective hold into a
// total one, a wwd that forgot flags would stop treating a release as a release.
func TestHoldFieldsSurviveARestart(t *testing.T) {
	r := newRig(t)
	if changed, err := r.st.AddException(holdExc()); err != nil || !changed {
		t.Fatalf("%v %v", changed, err)
	}
	r.reopen(t)
	got := r.st.Exceptions()
	if len(got) != 1 || !reflect.DeepEqual(got[0].Allow, holdExc().Allow) || !reflect.DeepEqual(got[0].Flags, holdExc().Flags) {
		t.Fatalf("after restart: %+v", got)
	}
	fe := r.st.Active()
	if len(fe) != 1 || !reflect.DeepEqual(fe[0].Allow, holdExc().Allow) || !reflect.DeepEqual(fe[0].Flags, holdExc().Flags) {
		t.Fatalf("facts after restart: %+v", fe)
	}
}

// The same hold again changes nothing (no event); a hold whose allow or flags differ is a change.
func TestHoldFieldsCountAsAChange(t *testing.T) {
	r := newRig(t)
	if _, err := r.st.AddException(holdExc()); err != nil {
		t.Fatal(err)
	}
	if changed, err := r.st.AddException(holdExc()); err != nil || changed {
		t.Fatalf("an identical hold: changed=%v err=%v", changed, err)
	}
	other := holdExc()
	other.Allow = &wire.HoldAllow{Classes: []wire.JobClass{wire.ClassMerge}, Repos: []string{}}
	if changed, err := r.st.AddException(other); err != nil || !changed {
		t.Fatalf("a different allow: changed=%v err=%v", changed, err)
	}
	flagged := other
	flagged.Flags = []wire.HoldFlag{wire.HoldFlagExpectPressure}
	if changed, err := r.st.AddException(flagged); err != nil || !changed {
		t.Fatalf("different flags: changed=%v err=%v", changed, err)
	}
}

// The facts and the list carry copies: a reader that changes what it was given does not change the hold.
func TestHoldFieldsAreCopiedOut(t *testing.T) {
	r := newRig(t)
	if _, err := r.st.AddException(holdExc()); err != nil {
		t.Fatal(err)
	}
	r.st.Active()[0].Allow.Repos[0] = "tampered"
	r.st.Exceptions()[0].Flags[0] = wire.HoldFlagExpectPressure
	if got := r.st.Exceptions()[0]; got.Allow.Repos[0] != "purdex" || got.Flags[0] != wire.HoldFlagRelease {
		t.Fatalf("the hold was changed from outside: %+v", got)
	}
}
