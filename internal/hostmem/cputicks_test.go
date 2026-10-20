package hostmem

import "testing"

func TestSumTicks(t *testing.T) {
	busy, total := SumTicks([]CoreTicks{{User: 10, System: 5, Idle: 80, Nice: 5}, {User: 1, System: 2, Idle: 3, Nice: 4}})
	if busy != 27 || total != 110 {
		t.Fatalf("busy %d total %d", busy, total)
	}
	if b, tot := SumTicks(nil); b != 0 || tot != 0 {
		t.Fatalf("no cores: %d %d", b, tot)
	}
}
