package testcmd

import (
	"reflect"
	"testing"
)

func TestTailLinesDropsTheFirstLineOnlyWhenTheTailWasCut(t *testing.T) {
	b := []byte("rtial\nfull 1\nfull 2\n")
	if got := tailLines(b, true, 200); !reflect.DeepEqual(got, []string{"full 1", "full 2"}) {
		t.Fatalf("cut: %q", got)
	}
	if got := tailLines(b, false, 200); !reflect.DeepEqual(got, []string{"rtial", "full 1", "full 2"}) {
		t.Fatalf("not cut: %q", got)
	}
	if got := tailLines(b, false, 2); !reflect.DeepEqual(got, []string{"full 1", "full 2"}) {
		t.Fatalf("last two: %q", got)
	}
	if tailLines(nil, true, 5) != nil {
		t.Fatal("nothing is nil")
	}
}
