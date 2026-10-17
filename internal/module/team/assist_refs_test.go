package teammod

import (
	"reflect"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// A ref a relay replaced is found through the lineage: the sessions whose chain holds it (assist plan contract 1).
func TestRefSuccessors(t *testing.T) {
	s := openTestStore(t)
	for i, ids := range [][4]string{{"op-0", "sid-0", "_aaaaaa", "sid-1"}, {"op-1", "sid-1", "_bbbbbb", "sid-2"}} {
		op := selfOp(ids[0], ids[1], ids[2], int64(1000*(i+1)))
		op.State = team.RelayClaimed
		if err := s.CreateRelayOp(op); err != nil {
			t.Fatal(err)
		}
		next := map[string]string{"sid-1": "_bbbbbb", "sid-2": "_cccccc"}[ids[3]]
		mustReport(t, s, ids[0], RelayReport{State: team.RelayCleared, NewSessionID: ids[3], NewRef: next, At: int64(1000*(i+1) + 500)})
	}
	got, err := s.RefSuccessors("_aaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"sid-2", "sid-1"}) {
		t.Fatalf("successors of _aaaaaa = %v", got)
	}
	if got, _ := s.RefSuccessors("_bbbbbb"); !reflect.DeepEqual(got, []string{"sid-2"}) {
		t.Fatalf("successors of _bbbbbb = %v", got)
	}
	if got, _ := s.RefSuccessors("_zzzzzz"); len(got) != 0 {
		t.Fatalf("unknown ref has successors %v", got)
	}
}
