package journal

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

// A pin (read-modify-write of the whole month file) and the drainer's appends (the same) both take lockFile, so neither
// overwrites the other: the pin survives every append and every append survives the pin. Mutation gate: update without
// lockFile → a round loses the pin or an entry (red).
func TestSetPinnedConcurrentWithUpsertKeepsBoth(t *testing.T) {
	for round := 0; round < 20; round++ {
		file := filepath.Join(t.TempDir(), "j.md")
		if err := Upsert(file, Entry{ID: "p:0", Kind: KindComm, At: t0, Text: "pin me"}); err != nil {
			t.Fatal(err)
		}
		const n = 12
		var wg sync.WaitGroup
		start := make(chan struct{})
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := SetPinned(file, "p:0", true); err != nil {
				t.Error(err)
			}
		}()
		for i := 1; i <= n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				if err := Upsert(file, Entry{ID: fmt.Sprintf("c:%d", i), Kind: KindComm, At: t0, Text: "x"}); err != nil {
					t.Error(err)
				}
			}(i)
		}
		close(start)
		wg.Wait()
		got, err := Parse(file)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != n+1 {
			t.Fatalf("round %d: kept %d of %d entries", round, len(got), n+1)
		}
		pinned := false
		for _, e := range got {
			if e.ID == "p:0" {
				pinned = e.Pinned
			}
		}
		if !pinned {
			t.Fatalf("round %d: the pin was overwritten by an append", round)
		}
	}
}
