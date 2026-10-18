package facts

import "testing"

func TestMergePSITakesTheLargerPerFieldAndNeedsEverySlice(t *testing.T) {
	sys := "some avg10=1.00 avg60=30.00 avg300=0.00 total=1\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n"
	usr := "some avg10=12.00 avg60=2.00 avg300=0.00 total=1\nfull avg10=3.00 avg60=0.00 avg300=0.00 total=0\n"
	p := MergePSI([]string{sys, usr})
	if p == nil || p.Some10 != 12 || p.Some60 != 30 || p.Full10 != 3 {
		t.Fatalf("larger per field: %+v", p)
	}
	// one slice under pressure but unreadable, the other at zero: unknown, not calm
	zero := "some avg10=0.00 avg60=0.00 avg300=0.00 total=0\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n"
	if got := MergePSI([]string{zero, ""}); got != nil {
		t.Errorf("an unreadable slice must make the answer unknown: %+v", got)
	}
	if got := MergePSI([]string{"garbage", zero}); got != nil {
		t.Errorf("an unparseable slice must make the answer unknown: %+v", got)
	}
}
