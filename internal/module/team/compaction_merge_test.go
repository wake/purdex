package teammod

import (
	"fmt"
	"sync"
	"testing"

	"github.com/wake/purdex/internal/team"
)

func TestMergeCompactionOverride_KeepsBothAndIsAtomic(t *testing.T) {
	s := openTestStore(t)
	if err := s.MergeCompactionOverride("a", team.CompactionOverride{Idle: ptr(false)}, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.MergeCompactionOverride("a", team.CompactionOverride{Threshold: ptr(50)}, 2); err != nil {
		t.Fatal(err)
	}
	if o, _ := s.CompactionOverride("a"); o.Idle == nil || *o.Idle || o.Threshold == nil || *o.Threshold != 50 {
		t.Fatalf("merge lost a field: %+v", o)
	}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		sid := fmt.Sprintf("c%d", i)
		for _, p := range []team.CompactionOverride{{Idle: ptr(false)}, {Threshold: ptr(40)}} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := s.MergeCompactionOverride(sid, p, 1); err != nil {
					t.Error(err)
				}
			}()
		}
	}
	wg.Wait()
	for i := 0; i < 50; i++ {
		if o, _ := s.CompactionOverride(fmt.Sprintf("c%d", i)); o.Idle == nil || o.Threshold == nil {
			t.Fatalf("session c%d lost a concurrent set: %+v", i, o)
		}
	}
}
